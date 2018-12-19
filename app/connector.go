package app

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/satori/go.uuid"
	comm_app "gitlab.com/frozy.io/connector/common"
	"gitlab.com/frozy.io/connector/config"
	"golang.org/x/crypto/ssh"
)

const (
	pollInterval                                = 15 * time.Second
	sshConnectionDefaultTimeout                 = 5 * time.Second
	providedApplicationConnectionDefaultTimeout = 5 * time.Second
)

var emptyUUID = uuid.UUID{}

type atomicStatusData int32

func (a *atomicStatusData) IsStatusOK() bool {
	val := atomic.LoadInt32((*int32)(a))
	return val == 1
}

func (a *atomicStatusData) StatusSet(isOK bool) {
	var val int32
	if isOK {
		val = 1
	}

	atomic.StoreInt32((*int32)(a), val)
}

// generic atomic status subsystem interface. Can be used for SSH connection state
// or for applications status signalling
type subsystemStatus interface {
	IsStatusOK() bool
	StatusSet(newState bool)
}

type sshRuntimeData struct {
	brokerConnStr   string
	sshCfg          *ssh.ClientConfig
	sshConn         ssh.Conn
	sshConnChannels <-chan ssh.NewChannel
	sshConnRequests <-chan *ssh.Request
	// connection status storage
	subsystemStatus
	// connector registration interface
	ConnectorRegisterIf
}

type registerAppData struct {
	accessToken string
	appName     comm_app.StructuredApplicationName
	appInfo     comm_app.ApplicationRegisterInfo
	appID       uuid.UUID // appID defined by broker after provided application advertising

	// provided application endpoint for connection
	connectTo string

	// sshConection for provided apps
	sshData sshRuntimeData

	// channel to control provider pooler (handle where all periodic work done)
	poolerControlChannel chan bool

	// application provide negotiated status storage
	subsystemStatus
}

// NOTE: Each provided/consumed Application have their own SSH connection to Broker
type intentAppData struct {
	accessToken        string
	sourceAppName      comm_app.StructuredApplicationName
	sourceAppInfo      comm_app.ApplicationIntentInfo
	destinationAppName comm_app.StructuredApplicationName
	destinationAppID   uuid.UUID // appID defined by broker after consume application possibility request

	// listener for consumed app
	listenAt string
	listener net.Listener

	// sshConection for consumed apps
	sshData sshRuntimeData

	// channel to control consumer pooler (handle where all periodic work done)
	poolerControlChannel chan bool

	// application consume negotiated status storage
	subsystemStatus
}

// Connector describe connector instance configuration
type Connector struct {
	// startup configuration
	Config config.Config

	// runtime configuration
	RsaKey *rsa.PrivateKey

	// register application runtime data for each broker connection
	// NOTE: at first stage we will use single connection
	registerApp []*registerAppData

	// intent application runtime data for each broker connection
	// NOTE: at first stage we will use single connection
	intentApp []*intentAppData

	// internal connector identifier used
	// for identification of runtime connector instance
	connectorIDLock sync.Mutex
	connectorID     uuid.UUID
}

// ConnectorRegisterIf implements interface for connector registration via
// any available connection to any broker in runtime
type ConnectorRegisterIf interface {
	// GetConnetcorID checks if connector ID from runtime registered on Broker and registers
	// current runtime connector instance if it is not registered yet
	RegisterConnectorID(sshConn ssh.Conn) error
}

// ParseIPHostFromString parses address string in format IP:Port into separate IP address (in string format) and port (as uint16)
func ParseIPHostFromString(addrStr string) (string, uint16, error) {
	ip, port, err := net.SplitHostPort(addrStr)
	if err != nil {
		return "", 0, err
	}
	portUint, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", 0, err
	}

	return ip, uint16(portUint), nil
}

// GetConnectorID is implementation of ConnectorRegisterIf interface
func (c *Connector) RegisterConnectorID(sshConn ssh.Conn) error {
	c.connectorIDLock.Lock()
	defer c.connectorIDLock.Unlock()

	// fill connector metadata and serialize it
	connRequest := &comm_app.JSONConnectorIDRegisterRequest{
		ConnectorID: c.connectorID.String(),
	}

	// get some external info about connector and fill its struct if connector just started
	if uuid.Equal(c.connectorID, uuid.Nil) {
		// do IP address parsing
		ip, _, err := ParseIPHostFromString(sshConn.LocalAddr().String())
		if err != nil {
			return fmt.Errorf("Can't parse connector remote address due to: %v", err)
		}

		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("Can't get connectors hostname due to: %v", err)
		}

		connRequest.ConnectorData.ConnectorName = c.Config.Frozy.ConnectorName
		connRequest.ConnectorData.ConnectorLocalIP = ip
		connRequest.ConnectorData.ConnectorHostname = hostname
		connRequest.ConnectorData.ConnectorOSName = runtime.GOOS
		connRequest.ConnectorData.ConnectorCloudName = "TBD"
	}

	payload, err := connRequest.ToStream()
	if err != nil {
		return err
	}

	// send global request and wait for answer
	isSupported, replyData, err := sshConn.SendRequest(comm_app.RegisterConnectorRequestType, true, payload)
	if !isSupported {
		if replyData == nil {
			return errors.New("Connector ID registration is unsupported by Broker")
		} else {
			errMsg, errLocal := comm_app.ErrorFromStream(replyData)
			if errLocal != nil {
				return fmt.Errorf("Connector ID registration can't be completed by Broker due to it's have unhandled error: (%v)", errLocal)
			} else {
				return fmt.Errorf("Connector ID registration is unhandled by Broker due to: %s", errMsg)
			}
		}
	} else if err != nil {
		return fmt.Errorf("Connector ID registration can't be completed due to: (%v)", err)
	} else if replyData == nil {
		return errors.New("Connector ID registration reply doesn't contain body. Can't process such reply ")
	}

	// get replyData from stream
	var connectorIDRegisterReply comm_app.JSONConnectorIDRegisterReply
	err = comm_app.FromStream(replyData, &connectorIDRegisterReply)
	if err != nil {
		return err
	}

	// check if Connector already registered
	rcvConnID, err := uuid.FromString(connectorIDRegisterReply.ConnectorID)
	if err != nil {
		return err
	}

	if !uuid.Equal(c.connectorID, uuid.Nil) {
		if !uuid.Equal(c.connectorID, rcvConnID) {
			return fmt.Errorf("Connector ID registration failed due to Broker replies with invalid ConnectorID. Expected: %s, received: %s",
				c.connectorID.String(), connectorIDRegisterReply.ConnectorID)
		}
	} else {
		if len(connectorIDRegisterReply.ConnectorID) == 0 {
			return errors.New("Connector ID registration failed due to Broker replies with empty ConnectorID")
		}
		// set new ConnectorID into runtime Connector config
		c.connectorID = rcvConnID
	}

	return nil
}

var globalConnector *Connector

// Execute the connector
func Execute(optionalConfig string) error {
	// create new connector instance
	if globalConnector == nil {
		globalConnector = new(Connector)
		if globalConnector == nil {
			panic("Can't create new Connector instance. Out of memory")
		}
	}

	globalConnector.Config.Load(optionalConfig)
	fmt.Printf("Initializing with:\n%s\n", globalConnector.Config.String())

	if err := globalConnector.initialize(); err != nil {
		return err
	}

	return globalConnector.run()
}

func (c *Connector) initialize() error {
	// Initialize RSA identity key and register it
	err := c.registerRSAKeyFingerprint()
	if err != nil {
		return err
	}

	// parse applications
	err = c.ParseConnectorApplications()
	if err != nil {
		return err
	}

	if len(c.registerApp) == 0 && len(c.intentApp) == 0 {
		return fmt.Errorf("No applications to process in config\n")
	}

	return nil
}

// ParseConnectorApplications does application configuration sanity check and parse
// application names into structured view ready to interface with broker
func (c *Connector) ParseConnectorApplications() error {
	sshConfig, err := c.sshClientConfig()
	if err != nil {
		return fmt.Errorf("Can't create SHH connection config for applications due to: (%v)", err)
	}

	for _, provAppVal := range c.Config.Applications.Applications {
		// some sanity checks at first stage
		if len(provAppVal.Name) == 0 {
			return errors.New("Empty name detected in provided application configuration")
		}
		if len(provAppVal.Host) == 0 || len(provAppVal.AccessToken) == 0 || provAppVal.Port == 0 {
			return fmt.Errorf("Empty host/port or access token detected in config of provided application %s", provAppVal.Name)
		}
		// ok, lets try to check application name
		appStructName, err := comm_app.DecodeApplicationString(comm_app.ApplicationNameString(provAppVal.Name))
		if err != nil {
			return fmt.Errorf("Can't decode application name due to: (%v)", err)
		}
		// create provider application runtime
		c.registerApp = append(c.registerApp, &registerAppData{
			sshData: sshRuntimeData{
				sshCfg:              sshConfig,
				brokerConnStr:       c.Config.Frozy.BrokerAddr().String(),
				subsystemStatus:     new(atomicStatusData),
				ConnectorRegisterIf: ConnectorRegisterIf(c),
			},
			accessToken: provAppVal.AccessToken,
			appName:     appStructName,
			appInfo: comm_app.ApplicationRegisterInfo{
				Host: provAppVal.Host,
				Port: provAppVal.Port,
			},
			connectTo: config.Endpoint{
				Host: provAppVal.Host,
				Port: provAppVal.Port,
			}.String(),
			poolerControlChannel: make(chan bool), // NOTE: For proper sync model we make this channel as unbuffered
			subsystemStatus:      new(atomicStatusData),
		})
	}

	for _, consAppVal := range c.Config.Applications.Intents {
		// some sanity checks at first stage
		if len(consAppVal.SrcName) == 0 || len(consAppVal.DstName) == 0 {
			return errors.New("Empty source or destination name detected in consumed application configuration")
		}
		if len(consAppVal.AccessToken) == 0 || consAppVal.Port == 0 {
			return fmt.Errorf("Empty port or access token detected in config of consumed application src:%s, dst: %s", consAppVal.SrcName, consAppVal.DstName)
		}
		// ok, lets try to check application names
		srcAppStructName, err := comm_app.DecodeApplicationString(comm_app.ApplicationNameString(consAppVal.SrcName))
		if err != nil {
			return fmt.Errorf("Can't decode application source name due to: (%v)", err)
		}
		dstAppStructName, err := comm_app.DecodeApplicationString(comm_app.ApplicationNameString(consAppVal.DstName))
		if err != nil {
			return fmt.Errorf("Can't decode application destination name due to: (%v)", err)
		}
		// create provider application runtime
		c.intentApp = append(c.intentApp, &intentAppData{
			sshData: sshRuntimeData{
				sshCfg:              sshConfig,
				brokerConnStr:       c.Config.Frozy.BrokerAddr().String(),
				subsystemStatus:     new(atomicStatusData),
				ConnectorRegisterIf: ConnectorRegisterIf(c),
			},
			accessToken:   consAppVal.AccessToken,
			sourceAppName: srcAppStructName,
			sourceAppInfo: comm_app.ApplicationIntentInfo{
				Port: consAppVal.Port,
			},
			destinationAppName:   dstAppStructName,
			listenAt:             fmt.Sprintf("0.0.0.0:%d", consAppVal.Port),
			poolerControlChannel: make(chan bool),
			subsystemStatus:      new(atomicStatusData),
		})
	}

	return nil
}

func (c Connector) sshClientConfig() (*ssh.ClientConfig, error) {
	signer, err := ssh.NewSignerFromKey(c.RsaKey)
	if err != nil {
		return nil, fmt.Errorf("Failed to build SSH auth method (%s)", err.Error())
	}

	return &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         sshConnectionDefaultTimeout,
	}, nil
}

func (c *Connector) run() error {
	fmt.Printf("Starting application processing threads: %d application(s) to provide, %d application(s) to consume\n",
		len(c.registerApp), len(c.intentApp))

	// run applications processing
	for _, provAppData := range c.registerApp {
		go provAppData.run()
	}

	for _, intAppData := range c.intentApp {
		go intAppData.run()
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan,
		os.Kill,
		os.Interrupt,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	s := <-sigChan
	fmt.Printf("application resources runner: received signal: %v\n", s)

	return nil
}

func (c *Connector) initIdentity() error {
	if privatePem, err := ioutil.ReadFile(c.Config.PrivateKeyPath()); err == nil {
		fmt.Println("Reading RSA key from", c.Config.PrivateKeyPath())
		privateBlock, _ := pem.Decode(privatePem)
		privateKey, err := x509.ParsePKCS1PrivateKey(privateBlock.Bytes)
		if err != nil {
			return err
		}
		c.RsaKey = privateKey
	} else if os.IsNotExist(err) {
		fmt.Println("Generating new RSA key to", c.Config.PrivateKeyPath())
		reader := rand.Reader
		key, err := rsa.GenerateKey(reader, config.RsaKeyBits)
		if err != nil {
			return err
		}
		c.RsaKey = key
		c.savePEMKey(c.Config.PrivateKeyPath(), c.Config.PublicKeyPath())
	} else if err != nil {
		return err
	}

	return nil
}

func (c *Connector) savePEMKey(privatePath string, publicPath string) error {
	privateFile, err := os.Create(privatePath)
	if err != nil {
		return err
	}
	defer privateFile.Close()
	privateFile.Chmod(0600)

	privateKeyPem := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(c.RsaKey),
	}

	err = pem.Encode(privateFile, privateKeyPem)
	if err != nil {
		return err
	}

	pub, err := ssh.NewPublicKey(&c.RsaKey.PublicKey)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(publicPath, ssh.MarshalAuthorizedKey(pub), 0644)
}

func (c *Connector) httpClient() *http.Client {
	tr := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: c.Config.Frozy.Insecure},
	}
	return &http.Client{Transport: tr}
}

func (c *Connector) registerRSAKeyFingerprint() error {
	if c.Config.Frozy.AccessToken == "" {
		return errors.New("Access token is not configured in Frozy config section")
	}

	err := c.initIdentity()
	if err != nil {
		return err
	}

	keyFile, err := os.Open(c.Config.PublicKeyPath())
	if err != nil {
		return err
	}
	defer keyFile.Close()

	bodyRequest := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyRequest)
	part, err := bodyWriter.CreateFormFile("key", filepath.Base(c.Config.PublicKeyPath()))
	if err != nil {
		return err
	}
	if _, err = io.Copy(part, keyFile); err != nil {
		return err
	}

	bodyWriter.WriteField("access-token", c.Config.Frozy.AccessToken)
	if err = bodyWriter.Close(); err != nil {
		return err
	}

	api := c.Config.Frozy.RegistrationURL()
	fmt.Printf("Register RSA key fingerprint at %s\n", api)

	request, err := http.NewRequest("POST", api, bodyRequest)
	if err != nil {
		return err
	}

	request.Header.Add("Content-Type", bodyWriter.FormDataContentType())

	// do request
	response, err := c.httpClient().Do(request)
	if response != nil {
		fmt.Println("API HTTP response:", response.Status)
		defer response.Body.Close()
	}

	if response == nil || response.StatusCode == 0 || err != nil {
		fmt.Println("Couldn't connect to Registration API. Are you using insecure tier (sandbox) " +
			`without FROZY_INSECURE environment variable set to "yes"?`)
		if err != nil {
			fmt.Println("Error: ", err.Error())
			return err
		}
		return fmt.Errorf(response.Status)
	}

	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusUnauthorized {
			fmt.Println("Most likely access Token is not valid. Try obtaining it from the frontend once again.")
		}
		return fmt.Errorf(response.Status)
	}

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}
	fmt.Println(string(body))

	return nil
}
