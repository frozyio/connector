package app

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/satori/go.uuid"
	log "github.com/sirupsen/logrus"
	comm_app "gitlab.com/frozy.io/connector/common"
	"gitlab.com/frozy.io/connector/config"
	"gitlab.com/frozy.io/connector/logconf"
	"golang.org/x/crypto/ssh"
)

const (
	brokersPollInterval                         = 15
	brokersTTL                                  = 3 * brokersPollInterval // broker`s entry TTL. If TTL exceeded entries will be throw out from cache
	listenFailTimeout                           = 5 * time.Second
	applicationRunInterval                      = 15 * time.Second
	brokerEntriesLifeTime                       = 30
	sshConnectionDefaultTimeout                 = 5 * time.Second
	providedApplicationConnectionDefaultTimeout = 5 * time.Second
	defaultMaxBrokerDiscoveryResponseSize       = 16 * 1024 * 1024 // 16 megabytes max
	defaultBrokerDiscoveryPath                  = "brokers"
	defaultApplicationDiscoveryPath             = "apps"
	defaultApplicationNameRequestField          = "app_structed_name"
	defaultDesiredConnectionToBrokerByApp       = 2
	defaultBlackListedBrokerAliveTime           = 60 * time.Second
)

var emptyUUID = uuid.UUID{}

type atomicStatusData int32

func (a *atomicStatusData) IsStatusOK() bool {
	val := atomic.LoadInt32((*int32)(a))
	return val == 1
}

func (a *atomicStatusData) StatusSet(newState bool) {
	var val int32
	if newState {
		val = 1
	}
	atomic.StoreInt32((*int32)(a), val)
}

func (a *atomicStatusData) StatusCheckAndSet(newState bool) bool {
	var val, valOld int32
	if newState {
		val = 1
	} else {
		valOld = 1
	}
	return atomic.CompareAndSwapInt32((*int32)(a), valOld, val)
}

// generic atomic status subsystem interface. Can be used for SSH connection state
// or for applications status signalling
type subsystemStatus interface {
	// atomically checks status
	IsStatusOK() bool
	// atomically sets status, but without garanties that state
	// changed for false to true or otherwise
	StatusSet(newState bool)
	// atomically sets status with garanties that state
	// changed by current moment
	StatusCheckAndSet(newState bool) bool
}

// BlackListedBrokerData stores data about temporary disabled brokers on connector
type BlackListedBrokerData struct {
	brokerData      comm_app.BrokerInfoData
	brokerAliveTime time.Time
}

// Connector describe connector instance configuration
type Connector struct {
	// atomically incremented connection counter
	// this counter used for paired connections enumeration and also can be used for
	// ordinary SSH connections enumeration that will useful if we will use
	// shared SSH connections between different applications
	conIDX uint64

	// flag is set when @broker@ section of config filled with
	// data. In this mode connector works without AD_ALB services usage
	staticBrokerMode bool

	// system logger
	logger *log.Entry

	// startup configuration
	config config.Config

	// SSH config used for all egress connections to broker
	sshCfg *ssh.ClientConfig

	// runtime configuration
	rsaKey *rsa.PrivateKey

	// applications runtime data for each broker connection
	// NOTE: at first stage we will use single connection
	applications []*applicationItemData

	// internal connector identifier used
	// for identification of runtime connector instance
	connectorIDLock sync.Mutex
	connectorID     uuid.UUID
	// black list of brokers
	brokersBlackList map[string]BlackListedBrokerData
	// internal Broker discovery service cache runtime data
	brokersListLock sync.Mutex
	// count of connections that must do each application to "best to connect" brokers
	desiredConnectionsCount int
	// storage contains "best to connect" Brokers list sorted by geo and load scores
	availableBrokers map[string]comm_app.BrokerInfoData
	// storage contains "best to connect" to registered Application Brokers list. This Broker lists is subsets of availableBrokers
	// key - fully qualified name of application gotten as comm_app.StructuredApplicationName.EncodeToString(), slice sorted by geo and load scores
	availableApplicationsBrokers map[string]map[string]comm_app.BrokerInfoData
	// stores received from ALB service public keys of brokers that must be checked by connector in SSH
	// connection processing
	brokerPublicKeysList map[string][]byte
}

// ConnectorDataIf implements methods for access to Connecotr mmetadata from applications
type ConnectorDataIf interface {
	// returns ConnecotrID registered on Brokers
	GetConnectorID() uuid.UUID
	// returns ALB enabled status
	GetALBEnabledStatus() bool
}

func (c *Connector) GetALBEnabledStatus() bool {
	return !c.staticBrokerMode
}

func (c *Connector) GetConnectorID() uuid.UUID {
	c.connectorIDLock.Lock()
	defer c.connectorIDLock.Unlock()

	return c.connectorID
}

// BrokerConnectionIf implements interface for connector that supply intents/registers applications
// with SSH connections to broker based on discovery data
type BrokerConnectionIf interface {
	// GetBrokerList returns list of Brokers from cache
	// function returns Broker list slice, desired connections per app and error code
	GetBrokersList(desiredApp comm_app.StructuredApplicationName) ([]comm_app.BrokerInfoData, int, error)
	// connects to desired broker and register connector
	// function returns PTR on established SSH connection to requested broker where Connector_ID already registered
	// connection ready to get requests
	ConnectToBroker(brokerData comm_app.BrokerInfoData) (*sshConnectionRuntime, error)
	// return new connection IDX
	GetNewConnectionIDX() uint64
	// adds broker into black list on defaultBlackListedBrokerAliveTime time from moment of adding
	AddBrokerToBlackList(brokerData comm_app.BrokerInfoData) error
	// filters broker list from black listed brokers
	FilterBlackListedBrokers(brokers []comm_app.BrokerInfoData) []comm_app.BrokerInfoData
}

// GetNewConnectionIDX returns atomically incremented Connector.conIDX that must be used for
// connection enumeration
func (c *Connector) GetNewConnectionIDX() uint64 {
	return atomic.AddUint64(&c.conIDX, 1)
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

func (c *Connector) registerConnectorID(sshConn ssh.Conn) error {
	c.connectorIDLock.Lock()
	defer c.connectorIDLock.Unlock()

	// get access token from value struct
	accessToken, err := c.config.Frozy.AccessToken.Value()
	if err != nil {
		return fmt.Errorf("Failed to get access token value: %v", err)
	}
	if string(accessToken) == "" {
		return errors.New("Access token is not configured in Frozy config section")
	}

	// fill connector metadata and serialize it
	connRequest := &comm_app.JSONConnectorIDRegisterRequest{
		ConnectorID: c.connectorID.String(),
		AuthInfo: comm_app.ApplicationActionRequestAuthInfo{
			AuthType:    comm_app.TrustUserAuthType,
			AccessToken: string(accessToken),
		},
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

		connRequest.ConnectorData.ConnectorName = c.config.Frozy.ConnectorName
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
				return fmt.Errorf("Connector ID registration can't be completed by Broker due to it's have unhandled error: %v", errLocal)
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

// AddBrokerToBlackList implements method of BrokerConnectionIf inteface
func (c *Connector) AddBrokerToBlackList(brokerData comm_app.BrokerInfoData) error {
	if len(brokerData.BrokerName) == 0 {
		return errors.New("Can't process entries with empty BrokerName")
	}

	c.brokersListLock.Lock()
	defer c.brokersListLock.Unlock()

	// check if broker not in Black list
	_, ok := c.brokersBlackList[brokerData.BrokerName]
	if ok {
		// Broker already Blacklisted
		return nil
	}

	// add Broker in list
	c.brokersBlackList[brokerData.BrokerName] = BlackListedBrokerData{
		brokerData:      brokerData,
		brokerAliveTime: time.Now().UTC().Add(defaultBlackListedBrokerAliveTime),
	}

	return nil
}

// FilterBlackListedBrokers implements method of BrokerConnectionIf inteface
func (c *Connector) FilterBlackListedBrokers(brokersToFilter []comm_app.BrokerInfoData) []comm_app.BrokerInfoData {
	c.brokersListLock.Lock()
	defer c.brokersListLock.Unlock()

	if len(brokersToFilter) == 0 {
		return brokersToFilter
	}

	result := brokersToFilter[:0]
	for _, brokerIt := range brokersToFilter {
		isBrFound := false
		for brName, _ := range c.brokersBlackList {
			if brName == brokerIt.BrokerName {
				//				fmt.Printf("FOUND blacklisted broker %s, delete them\n", brName)
				isBrFound = true
			}
		}
		if !isBrFound {
			result = append(result, brokerIt)
		}
	}

	return result
}

// GetBrokersList implemets method of BrokerConnectionIf interface
func (c *Connector) GetBrokersList(desiredApp comm_app.StructuredApplicationName) ([]comm_app.BrokerInfoData, int, error) {
	c.brokersListLock.Lock()
	defer c.brokersListLock.Unlock()

	var desiredConnectionsCount int
	var resultBrokerList []comm_app.BrokerInfoData

	if c.desiredConnectionsCount > 0 {
		desiredConnectionsCount = c.desiredConnectionsCount
	} else {
		desiredConnectionsCount = defaultDesiredConnectionToBrokerByApp
	}

	if len(desiredApp.Name) > 0 {
		encDesiredApp, err := desiredApp.EncodeToString()
		if err != nil {
			return resultBrokerList, 0, fmt.Errorf("Can't encode application %s for use it as map key", desiredApp.ShortAppName())
		}

		brokersList, ok := c.availableApplicationsBrokers[encDesiredApp]
		if ok {
			// reload brokers from map
			for _, brokerIt := range brokersList {
				resultBrokerList = append(resultBrokerList, brokerIt)
			}
		} else {
			return resultBrokerList, 0, fmt.Errorf("Requested Brokers list for application %s doesn't exists", desiredApp.ShortAppName())
		}
	} else {
		// reload brokers from map
		for _, brokerIt := range c.availableBrokers {
			resultBrokerList = append(resultBrokerList, brokerIt)
		}
	}

	// sorting brokers list
	resultBrokerList = BrokersListSort(resultBrokerList)

	return resultBrokerList, desiredConnectionsCount, nil
}

// ConnectToBroker implemets method of BrokerConnectionIf interface
func (c *Connector) ConnectToBroker(brokerData comm_app.BrokerInfoData) (*sshConnectionRuntime, error) {
	// some sanity checks
	err := c.sanityCheckFoundedBroker([]comm_app.BrokerInfoData{brokerData})
	if err != nil {
		return nil, err
	}

	brokerConnectionData := config.Endpoint{
		Host: brokerData.BrokerIP.String(),
		Port: brokerData.BrokerPort,
	}

	// register Brokers public key in storage
	c.brokersListLock.Lock()
	c.brokerPublicKeysList[brokerConnectionData.String()] = brokerData.BrokerPublicKey
	c.brokersListLock.Unlock()

	//	fmt.Printf("Try to dial to %s with timeout %d\n", brokerConnectionData.String(), c.sshCfg.Timeout)

	// create network connection
	conn, err := net.DialTimeout("tcp", brokerConnectionData.String(), c.sshCfg.Timeout)
	if err != nil {
		return nil, err
	}

	//	fmt.Printf("Dial to %s with timeout %d is OK\n", brokerConnectionData.String(), c.sshCfg.Timeout)

	// create SSH client connection
	sshConnData := &sshConnectionRuntime{
		brokerData:  &brokerData,
		pairedConns: make(map[uint64]*PairConnItem),
	}

	// set I/O deadline
	err = conn.SetDeadline(time.Now().Add(c.sshCfg.Timeout))
	if err != nil {
		conn.Close()
		return nil, err
	}

	// connects to Broker
	sshConnData.sshConn, sshConnData.sshConnChannels, sshConnData.sshConnRequests, err = ssh.NewClientConn(conn, brokerConnectionData.String(), c.sshCfg)
	if err != nil {
		return nil, err
	}

	// reset I/O deadline to zero
	err = conn.SetDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return nil, err
	}

	// now, trying to advertise ConnectorID with Broker
	err = c.registerConnectorID(sshConnData.sshConn)
	if err != nil {
		sshConnData.sshConn.Close()
		return nil, err
	}

	// set success state for connection
	sshConnData.sshConnStatus.StatusSet(true)

	return sshConnData, nil
}

var globalConnector *Connector

// Execute the connector
func Execute(optionalConfig config.ConnectorCmdLineArgs) error {
	// create new connector instance
	if globalConnector == nil {
		globalConnector = new(Connector)
		if globalConnector == nil {
			panic("Can't create new Connector instance. Out of memory")
		}

		// create map inside connector
		globalConnector.brokersBlackList = make(map[string]BlackListedBrokerData)
		globalConnector.availableBrokers = make(map[string]comm_app.BrokerInfoData)
		globalConnector.availableApplicationsBrokers = make(map[string]map[string]comm_app.BrokerInfoData)
		globalConnector.brokerPublicKeysList = make(map[string][]byte)
	}

	// load config from file and environmet variables
	globalConnector.config.Load(optionalConfig)

	// init logging subsystem and register it
	logger, err := logconf.FrozyInitLogging()
	if err != nil {
		return fmt.Errorf("Can't init logging subsystem due to: %v", err)
	}
	SetupGlobalSystemLogger(logger)

	tier, err := globalConnector.config.Frozy.Tier.Value()
	if err != nil {
		return fmt.Errorf("Can't get system TIER value due to: %v", err)
	}

	// set logger convinient fields
	globalConnector.logger = GetSystemLogger().WithFields(log.Fields{
		"connector_name": globalConnector.config.Frozy.ConnectorName,
		"tier":           string(tier),
		"insecure":       globalConnector.config.Frozy.Insecure,
	})

	// store previous value before show to user
	tmpAccessToken := globalConnector.config.Frozy.AccessToken
	globalConnector.config.Frozy.AccessToken = config.LiteralString("Whops...")

	globalConnector.logger.Debugf("Initializing with:\n%s\n", globalConnector.config.String())
	globalConnector.config.Frozy.AccessToken = tmpAccessToken

	globalConnector.logger.Debugf("Resolving secrets in configuration...")
	globalConnector.config.ResolveRemoteValuesUntilSuccess(globalConnector.logger)

	if err = globalConnector.initialize(); err != nil {
		return err
	}

	// check what mode connector must use and run system
	// NOTE: Broker single mode works only if user manually configure Static Broker section
	if !reflect.DeepEqual(&globalConnector.config.Frozy.BrokerStatic, &config.Endpoint{}) {
		globalConnector.staticBrokerMode = true

		brHost, err := globalConnector.config.Frozy.BrokerAddr()
		if err != nil {
			return err
		}

		addrs, err := net.LookupHost(brHost.Host)
		if err != nil {
			return fmt.Errorf("Can't resolve single broker Host name due to: %v", err)
		}

		globalConnector.logger.Debugf("Single Broker host`s addresses: %v", addrs)

		// trying to parse addresses into IP
		var ipAddr net.IP
		for _, addrIt := range addrs {
			ipAddr = net.ParseIP(addrIt)
			if ipAddr != nil {
				break
			}
		}

		if len(ipAddr) == 0 {
			return fmt.Errorf("Can't parse Broker`s host address into IP")
		}

		// fill available brokers list
		globalConnector.availableBrokers["SingleBroker"] = comm_app.BrokerInfoData{
			BrokerName:      "SingleBroker",
			BrokerIP:        ipAddr,
			BrokerPort:      brHost.Port,
			BrokerGeoScore:  1,
			BrokerLoadScore: 1,
		}
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

	if len(c.applications) == 0 {
		return fmt.Errorf("No applications to process in config")
	}

	return nil
}

// ParseConnectorApplications does application configuration sanity check and parse
// application names into structured view ready to interface with broker
func (c *Connector) ParseConnectorApplications() error {
	var err error

	// get access token from value struct
	accessToken, err := c.config.Frozy.AccessToken.Value()
	if err != nil {
		return fmt.Errorf("Failed to get access token value : %v", err)
	}
	if string(accessToken) == "" {
		return errors.New("Access token is not configured in Frozy config section")
	}

	c.sshCfg, err = c.sshClientConfig()
	if err != nil {
		return fmt.Errorf("Can't create SHH connection config for applications due to: (%v)", err)
	}

	for _, provAppVal := range c.config.Applications {
		// some sanity checks at first stage
		if len(provAppVal.Name) == 0 {
			return errors.New("Empty name detected in provided application configuration")
		}
		if len(provAppVal.Host) == 0 || provAppVal.Port == 0 {
			return fmt.Errorf("Empty host/port or access token detected in config of provided application %s", provAppVal.Name)
		}
		// ok, lets try to check application name
		appStructName, err := comm_app.DecodeApplicationString(comm_app.ApplicationNameString(provAppVal.Name))
		if err != nil {
			return fmt.Errorf("Can't decode application name due to: (%v)", err)
		}

		regLogger := c.logger.WithFields(log.Fields{
			"reg_app_name":  appStructName.ShortAppName(),
			"reg_app_owner": appStructName.Owner,
			"reg_app_host":  provAppVal.Host,
			"reg_app_port":  provAppVal.Port,
		})

		// create provider application runtime
		c.applications = append(c.applications, &applicationItemData{
			emergencyStopChannel: make(chan bool, 1),
			connMetaData:         ConnectorDataIf(c),
			logger:               regLogger,
			accessToken:          string(accessToken),
			appName:              appStructName,
			appRegInfo: comm_app.ApplicationRegisterInfo{
				Host: provAppVal.Host,
				Port: provAppVal.Port,
			},
			connectTo: config.Endpoint{
				Host: provAppVal.Host,
				Port: provAppVal.Port,
			}.String(),
			appSSHStorage: ApplicationSSHConnectionsStorage{
				sshConnectorIf: BrokerConnectionIf(c),
				sshConnections: make(map[string]*sshConnectionRuntime),
			},
		})
	}

	for _, consAppVal := range c.config.Intents {
		// some sanity checks at first stage
		if len(consAppVal.SrcName) == 0 || len(consAppVal.DstName) == 0 {
			return errors.New("Empty source or destination name detected in consumed application configuration")
		}
		if len(consAppVal.Host) == 0 {
			return fmt.Errorf("Empty host detected in config of consumed application src:%s, dst: %s", consAppVal.SrcName, consAppVal.DstName)
		}
		if consAppVal.Port == 0 {
			return fmt.Errorf("Empty port detected in config of consumed application src:%s, dst: %s", consAppVal.SrcName, consAppVal.DstName)
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

		intLogger := c.logger.WithFields(log.Fields{
			"intent_src_app_name":  srcAppStructName.ShortAppName(),
			"intent_src_app_owner": srcAppStructName.Owner,
			"intent_dst_app_name":  dstAppStructName.ShortAppName(),
			"intent_dst_app_owner": dstAppStructName.Owner,
			"intent_app_host":      consAppVal.Host,
			"intent_app_port":      consAppVal.Port,
		})

		// create provider application runtime
		c.applications = append(c.applications, &applicationItemData{
			emergencyStopChannel: make(chan bool, 1),
			connMetaData:         ConnectorDataIf(c),
			appIntentType:        true,
			logger:               intLogger,
			accessToken:          string(accessToken),
			sourceAppName:        srcAppStructName,
			sourceAppRegInfo: comm_app.ApplicationIntentInfo{
				Host: consAppVal.Host,
				Port: consAppVal.Port,
			},
			destinationAppName: dstAppStructName,
			listenAt:           fmt.Sprintf("%s:%d", consAppVal.Host, consAppVal.Port),
			appSSHStorage: ApplicationSSHConnectionsStorage{
				sshConnectorIf: BrokerConnectionIf(c),
				sshConnections: make(map[string]*sshConnectionRuntime),
			},
		})
	}

	return nil
}

func (c Connector) serverKeyCheckCallback(connStr string, remoteAddr net.Addr, publicKey ssh.PublicKey) error {
	c.brokersListLock.Lock()
	defer c.brokersListLock.Unlock()

	// trying to get registered public key for current Broker
	brPubKey, ok := c.brokerPublicKeysList[connStr]
	if !ok {
		c.logger.Warnf("Public key for Broker host %s isn't registered", connStr)
		return fmt.Errorf("Public key for Broker host %s isn't registered", connStr)
	}

	srvPubKey := publicKey.Marshal()

	if !reflect.DeepEqual(&brPubKey, &srvPubKey) {
		c.logger.Warnf("Public key for Broker host %s is invalid", connStr)
		return fmt.Errorf("Public key for Broker host %s is invalid", connStr)
	}

	return nil
}

func (c Connector) sshClientConfig() (*ssh.ClientConfig, error) {
	signer, err := ssh.NewSignerFromKey(c.rsaKey)
	if err != nil {
		return nil, fmt.Errorf("Failed to build SSH auth method (%s)", err.Error())
	}

	return &ssh.ClientConfig{
		HostKeyCallback: c.serverKeyCheckCallback,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		Timeout:         sshConnectionDefaultTimeout,
	}, nil
}

func (c *Connector) run() error {
	c.logger.Infof("Starting application processing threads: %d application(s) to process", len(c.applications))

	// statring broker discovery thread
	go c.brokerDiscoveryThread()

	// run applications processing
	for _, appData := range c.applications {
		go appData.run()
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
	c.logger.Infof("Application resources runner: received signal: %v", s)

	return nil
}

func (c *Connector) initIdentity() error {
	if privatePem, err := ioutil.ReadFile(c.config.PrivateKeyPath()); err == nil {
		c.logger.Debugf("Reading RSA key from: %s", c.config.PrivateKeyPath())
		privateBlock, _ := pem.Decode(privatePem)
		privateKey, err := x509.ParsePKCS1PrivateKey(privateBlock.Bytes)
		if err != nil {
			return err
		}
		c.rsaKey = privateKey
	} else if os.IsNotExist(err) {
		c.logger.Debugf("Generating new RSA key to: %s", c.config.PrivateKeyPath())
		reader := rand.Reader
		key, err := rsa.GenerateKey(reader, config.RsaKeyBits)
		if err != nil {
			return err
		}
		c.rsaKey = key
		c.savePEMKey(c.config.PrivateKeyPath(), c.config.PublicKeyPath())
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
		Bytes: x509.MarshalPKCS1PrivateKey(c.rsaKey),
	}

	err = pem.Encode(privateFile, privateKeyPem)
	if err != nil {
		return err
	}

	pub, err := ssh.NewPublicKey(&c.rsaKey.PublicKey)
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
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: c.config.Frozy.Insecure},
	}
	return &http.Client{Transport: tr}
}

// ******************************* SORT IMPLEMENTATION for []comm_app.BrokerInfoData slice ************************
type lessFunc func(p1, p2 *comm_app.BrokerInfoData) bool

type multiSorter struct {
	brokers []comm_app.BrokerInfoData
	less    []lessFunc
}

func (ms *multiSorter) Sort(brokers []comm_app.BrokerInfoData) {
	ms.brokers = brokers
	sort.Sort(ms)
}

// OrderedBy returns a Sorter that sorts using the less functions, in order.
// Call its Sort method to sort the data.
func BrokersOrderedBy(less ...lessFunc) *multiSorter {
	return &multiSorter{
		less: less,
	}
}

// Len is part of sort.Interface.
func (ms *multiSorter) Len() int {
	return len(ms.brokers)
}

// Swap is part of sort.Interface.
func (ms *multiSorter) Swap(i, j int) {
	ms.brokers[i], ms.brokers[j] = ms.brokers[j], ms.brokers[i]
}

// Less is part of sort.Interface. It is implemented by looping along the
// less functions until it finds a comparison that discriminates between
// the two items (one is less than the other).
// Note that it can call the less functions twice per call.
func (ms *multiSorter) Less(i, j int) bool {
	p, q := &ms.brokers[i], &ms.brokers[j]
	// Try all but the last comparison.
	var k int
	for k = 0; k < len(ms.less)-1; k++ {
		less := ms.less[k]
		switch {
		case less(p, q):
			// p < q, so we have a decision.
			return true
		case less(q, p):
			// p > q, so we have a decision.
			return false
		}
		// p == q; try the next comparison.
	}
	// All comparisons to here said "equal", so just return whatever the final comparison reports
	return ms.less[k](p, q)
}

// *****************************************************************************************************************

// API sorts brokers in list by scores in ASC order
func BrokersListSort(inLst []comm_app.BrokerInfoData) []comm_app.BrokerInfoData {
	// Helper functions that orders the BrokerInfoData structure
	geoScores := func(c1, c2 *comm_app.BrokerInfoData) bool {
		return c1.BrokerGeoScore < c2.BrokerGeoScore
	}
	loadScores := func(c1, c2 *comm_app.BrokerInfoData) bool {
		return c1.BrokerLoadScore < c2.BrokerLoadScore
	}

	// we will use geo scores as higher priority scores
	BrokersOrderedBy(geoScores, loadScores).Sort(inLst)

	return inLst
}

func (c *Connector) sanityCheckFoundedBroker(broker []comm_app.BrokerInfoData) error {
	for _, brokerIt := range broker {
		// some sanity checks here
		if len(brokerIt.BrokerName) == 0 {
			return fmt.Errorf("Discovered Broker name is empty in discovered item %+v", brokerIt)
		}
		if len(brokerIt.BrokerIP.String()) == 0 {
			return fmt.Errorf("Discovered Broker host is empty in discovered item %+v", brokerIt)
		}
		if brokerIt.BrokerPort == 0 {
			return fmt.Errorf("Discovered Broker port is empty in discovered item %+v", brokerIt)
		}
	}
	return nil
}

// API perform Broker lists postprocessing. It finds Brokers that conform to desired application list and best to connect
func (c *Connector) brokerListUpdate(brokersAll []comm_app.BrokerInfoData, brokersApps map[string][]comm_app.BrokerInfoData) {
	c.brokersListLock.Lock()
	defer c.brokersListLock.Unlock()

	// do reload generic brokers into cache
	for _, brokerIt := range brokersAll {
		// set Broker TTL
		brokerIt.BrokerTTL = time.Now().UTC().Add(time.Duration(brokersTTL) * time.Second)
		c.availableBrokers[brokerIt.BrokerName] = brokerIt
	}

	// check if there entries with exceeded TTL
	timeNow := time.Now().UTC()
	for brokerNameIt, brokerIt := range c.availableBrokers {
		if timeNow.Sub(brokerIt.BrokerTTL) > 0 {
			c.logger.Debugf("Delete aged broker %s from negotiated list of brokers", brokerNameIt)

			// delete aged broker from list
			delete(c.availableBrokers, brokerNameIt)
		}
	}

	// do reload application specific brokers into cache
	for appNameIt, brokerAppsIt := range brokersApps {
		// check if map is empty
		if c.availableApplicationsBrokers[appNameIt] == nil {
			c.availableApplicationsBrokers[appNameIt] = make(map[string]comm_app.BrokerInfoData)
		}

		for _, brokerIt := range brokerAppsIt {
			// set Broker TTL
			brokerIt.BrokerTTL = time.Now().UTC().Add(time.Duration(brokersTTL) * time.Second)
			c.availableApplicationsBrokers[appNameIt][brokerIt.BrokerName] = brokerIt
		}
	}

	// check if there entries with exceeded TTL
	timeNow = time.Now().UTC()
	for appNameIt, brokerAppsIt := range c.availableApplicationsBrokers {
		for brokerNameIt, brokerIt := range brokerAppsIt {
			if timeNow.Sub(brokerIt.BrokerTTL) > 0 {
				c.logger.Debugf("Delete aged broker %s from negotiated list of brokers", brokerNameIt)

				// delete aged broker from list
				delete(c.availableApplicationsBrokers[appNameIt], brokerNameIt)
			}
		}
	}
}

// API gets list of applications that set as destinations in list of registered Intents
func (c *Connector) getIntentsDstAppsList() (map[string]string, error) {
	resultMap := make(map[string]string)

	// go through all intents and get dst apps with unique filtering
	for _, appIt := range c.applications {
		if !appIt.appIntentType {
			continue
		}

		encAppName, err := appIt.destinationAppName.EncodeToString()
		if err != nil {
			return nil, fmt.Errorf("Can't encode app name (%+v) to string due to: %v", appIt.destinationAppName, err)
		}

		// reload access token of intent app
		resultMap[encAppName] = appIt.accessToken
	}

	return resultMap, nil
}

func (c *Connector) refreshBrokersBlackList() {
	c.brokersListLock.Lock()
	defer c.brokersListLock.Unlock()

	timeNow := time.Now().UTC()

	for brName, brData := range c.brokersBlackList {
		// delete entry if time is up
		if brData.brokerAliveTime.Sub(timeNow) < 0 {

			c.logger.Debugf("Broker %s (%s.%d) deleted from Black list",
				brData.brokerData.BrokerName,
				brData.brokerData.BrokerIP.String(),
				brData.brokerData.BrokerPort)

			delete(c.brokersBlackList, brName)
		}
	}
}

func (c *Connector) brokerDiscoveryThread() {
	c.logger.Debug("Broker discovery thread started")

	// first timeout inited to 1 ns. We must discovery brokers at startup
	timeoutDiscovery := time.After(time.Millisecond)
	timeoutBlackList := time.After(time.Second)

	for {
		select {
		case <-timeoutDiscovery:
			// rearm timeout to next broker discovery
			timeoutDiscovery = time.After(time.Duration(brokersPollInterval) * time.Second)

			if c.staticBrokerMode {
				continue
			}

			// update all Brokers list
			brokersAll, err := c.brokerDiscovery(comm_app.StructuredApplicationName{}, "")
			if err != nil {
				c.logger.Warnf("Can't complete generic Broker discovery process due to: %v. Next try after some time interval", err)
			} else {
				err = c.sanityCheckFoundedBroker(brokersAll)
				if err != nil {
					c.logger.Warnf("Discovered Brokers discarded to process due to: %v, skip it", err)
				}
			}

			// NOTE: we doesn't sort brokers here. This operation will be done later when brokers will be upload from cache

			// construct desired applications Broker`s list
			appBrList := make(map[string][]comm_app.BrokerInfoData)
			appList, err := c.getIntentsDstAppsList()
			if err != nil {
				c.logger.Warnf("Discovered App Brokers discarded to process due to: %v, skip it", err)
			} else {
				for appName, appAccToken := range appList {
					structAppName, err := comm_app.DecodeApplicationString(comm_app.ApplicationNameString(appName))
					if err != nil {
						c.logger.Errorf("Can't decode application string %s due to: %v", appName, err)
						continue
					}

					brokersApp, err := c.brokerDiscovery(structAppName, appAccToken)
					if err != nil {
						c.logger.Warnf("Can't complete Broker Application %s discovery process due to: %v", structAppName.ShortAppName(), err)
						continue
					}

					// store resulted Broker`s current application list into map
					if len(brokersApp) > 0 {
						err = c.sanityCheckFoundedBroker(brokersApp)
						if err != nil {
							c.logger.Errorf("Discovered App Brokers discarded to process due to: %v, skip it", err)
							continue
						}

						// and store it into map
						appBrList[appName] = brokersApp
					}
				}
			}

			// do Brokers search results postprocessing
			c.brokerListUpdate(brokersAll, appBrList)

		case <-timeoutBlackList:
			// delete aged Brokers from list and allow to connect to them
			c.refreshBrokersBlackList()

			// rearm timeout to next broker refresh
			timeoutBlackList = time.After(time.Second)

			// some other channels added here
		}
	}

	c.logger.Debug("Broker discovery thread stopped")
}

// API performs request from Broker_ALB service list of Brokers regustered on system or list of Brokers that have desired application registration
// this API used by internal thread that performs Broker list refresh tasks
func (c *Connector) brokerDiscovery(appData comm_app.StructuredApplicationName, appAccToken string) ([]comm_app.BrokerInfoData, error) {
	var brokers []comm_app.BrokerInfoData

	// get access token from value struct
	accessToken, err := c.config.Frozy.AccessToken.Value()
	if err != nil {
		return brokers, fmt.Errorf("Failed to get access token value : %v", err)
	}
	if string(accessToken) == "" {
		return brokers, errors.New("Access token is not configured in Frozy config section")
	}

	// create API URLs and add nesseccary fields into request
	api, err := c.config.Frozy.BrokerDiscoveryURL()
	if err != nil {
		return brokers, fmt.Errorf("Failed to get BrokerDiscovery URL value : %v", err)
	}

	if len(appData.Name) == 0 {
		api += defaultBrokerDiscoveryPath
	} else {
		api += defaultApplicationDiscoveryPath
	}

	// create new request
	request, err := http.NewRequest("GET", api, nil)
	if err != nil {
		return brokers, err
	}

	// create API URLs and add nesseccary fields into request
	if len(appData.Name) == 0 {
		// insert global AccessToken
		request.Header.Set(c.config.BrokerDiscoveryAccessTokenName(), string(accessToken))

		c.logger.Debugf("Get Brokers list at %s", api)
	} else {
		appJSON, err := json.Marshal(&appData)
		if err != nil {
			return brokers, err
		}

		//		fmt.Printf("appJSON: %+v\n", string(appJSON))

		// add app_struct_name to query parameters
		query := request.URL.Query()
		query.Add(defaultApplicationNameRequestField, string(appJSON))
		request.URL.RawQuery = query.Encode()

		// NOTE: StructuredApplicationName doesn't contain real App Owner UUID
		// this structure only contain literal owner name that can be equal to self or contain real app owner e-mail
		// Fully qualify App name resolution will be done on broker_alb service and we must do requests there with
		// Intent Access-Token usage

		// insert Intent`s AccessToken
		request.Header.Set(c.config.BrokerDiscoveryAccessTokenName(), appAccToken)

		c.logger.Debugf("Get Brokers list at %s, app_name: %s, app_owner: %s",
			api, appData.ShortAppName(), appData.Owner)
	}

	// do request
	response, err := c.httpClient().Do(request)
	if response != nil {
		c.logger.Debugf("API HTTP response: %s", response.Status)
		defer response.Body.Close()
	}

	if response == nil || response.StatusCode == 0 || err != nil {
		c.logger.Warn("Couldn't connect to Broker Discovery service")
		if err != nil {
			c.logger.Errorf("Error: %v", err)
			return brokers, err
		}
		return brokers, fmt.Errorf(response.Status)
	}

	if response.StatusCode != http.StatusOK {
		return brokers, fmt.Errorf(response.Status)
	}

	limitedReader := &io.LimitedReader{
		R: response.Body,
		N: defaultMaxBrokerDiscoveryResponseSize,
	}

	// create stream JSON decoder for limited in size response
	decoder := json.NewDecoder(limitedReader)
	err = decoder.Decode(&brokers)
	if err != nil {
		return brokers, err
	}

	c.logger.Debugf("Readed broker discovery data: %+v", brokers)

	return brokers, nil
}

func (c *Connector) registerRSAKeyFingerprint() error {
	accessToken, err := c.config.Frozy.AccessToken.Value()
	if err != nil {
		return fmt.Errorf("Failed to get access token value : %v", err)
	}
	if string(accessToken) == "" {
		return errors.New("Access token is not configured in Frozy config section")
	}

	err = c.initIdentity()
	if err != nil {
		return err
	}

	keyFile, err := os.Open(c.config.PublicKeyPath())
	if err != nil {
		return err
	}
	defer keyFile.Close()

	bodyRequest := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyRequest)
	part, err := bodyWriter.CreateFormFile("key", filepath.Base(c.config.PublicKeyPath()))
	if err != nil {
		return err
	}
	if _, err = io.Copy(part, keyFile); err != nil {
		return err
	}

	bodyWriter.WriteField(c.config.RegistrationAccessTokenName(), string(accessToken))
	if err = bodyWriter.Close(); err != nil {
		return err
	}

	api, err := c.config.Frozy.RegistrationURL()
	if err != nil {
		return fmt.Errorf("Failed to get registration URL: %v", err)
	}

	c.logger.Debugf("Register RSA key fingerprint at %s", api)

	request, err := http.NewRequest("POST", api, bodyRequest)
	if err != nil {
		return err
	}

	request.Header.Add("Content-Type", bodyWriter.FormDataContentType())

	// do request
	response, err := c.httpClient().Do(request)
	if response != nil {
		c.logger.Debugf("API HTTP response: %s", response.Status)
		defer response.Body.Close()
	}

	if response == nil || response.StatusCode == 0 || err != nil {
		c.logger.Warn("Couldn't connect to Registration API. Are you using insecure tier (sandbox) without FROZY_INSECURE environment variable set to \"yes\"?")
		if err != nil {
			c.logger.Errorf("Error: %v", err)
			return err
		}
		return fmt.Errorf(response.Status)
	}

	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusUnauthorized {
			c.logger.Info("Most likely Access Token is not valid. Try obtaining it from the frontend once again.")
		}
		return fmt.Errorf(response.Status)
	}

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}

	c.logger.Debugf("Responce body: %s", string(body))

	return nil
}

// globalLogger implements system whole logging subsystem
var globalLogger *log.Logger

// SetupGlobalSystemLogger stores global system logger
func SetupGlobalSystemLogger(logger *log.Logger) {
	globalLogger = logger
}

func GetSystemLogger() *log.Logger {
	// GetSystemLogger returns system wide logger
	if globalLogger == nil {
		panic("Global system wide logger doesn't inited yet")
	}

	return globalLogger
}
