package app

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"gopkg.in/ini.v1"
)

const rsaKeyBits = 2048

const remoteResourseDefaultPort = 1025

// Role is for connector
type Role string

const (
	provider Role = "provider"
	consumer Role = "consumer"
)

// Endpoint is just a pair of string and uint16
type Endpoint struct {
	Host string
	Port uint16
}

// Network ...
func (addr Endpoint) Network() string { return "tcp" }
func (addr Endpoint) String() string  { return fmt.Sprintf("%s:%d", addr.Host, addr.Port) }

// ConnectorConfig if for application configuration
type ConnectorConfig struct {
	Role     Role
	Resource string
	Farm     string
	Addr     Endpoint
}

// RegistrationConfig ...
type RegistrationConfig struct {
	Root      string
	Path      string
	Insecure  bool
	JoinToken string
}

// InitConfig is for application initialization
type InitConfig struct {
	Registration RegistrationConfig
	Broker       Endpoint
	ConfigDir    string
}

// Config is a Connector configuration
type Config struct {
	Init      InitConfig
	RsaKey    *rsa.PrivateKey
	Connector ConnectorConfig
}

func (conf Config) privateKeyPath() string {
	return path.Join(conf.Init.ConfigDir, "id_rsa")
}

func (conf Config) publicKeyPath() string {
	return conf.privateKeyPath() + ".pub"
}

func (conf Config) configPath() string {
	return path.Join(conf.Init.ConfigDir, "config")
}

func (conf Config) remoteResourse() Endpoint {
	return Endpoint{conf.Connector.Farm + "." + conf.Connector.Resource, remoteResourseDefaultPort}
}

// ExecuteConnector the connectioor
func (conf *Config) ExecuteConnector() error {
	var err error

	// Create config dir if not exists
	if err = os.MkdirAll(conf.Init.ConfigDir, os.ModeDir|0775); err == nil || os.IsExist(err) {
		// Initialize RSA identity key
		if err = conf.initIdentity(); err == nil {
			// Initialize the connectior config
			if err = conf.initConfig(); err == nil {
				fmt.Printf("Connector initialized as %+v\n", conf.Connector)
				switch conf.Connector.Role {
				case consumer:
					err = runConsumer(conf)
				case provider:
					err = runProvider(conf)
				default:
					err = fmt.Errorf("Invalid connector role %s", conf.Connector.Role)
				}
			}
		}
	}

	return err
}

func (conf *Config) initIdentity() error {
	if privatePem, err := ioutil.ReadFile(conf.privateKeyPath()); err == nil {
		fmt.Println("Reading RSA key from", conf.privateKeyPath())
		privateBlock, _ := pem.Decode(privatePem)
		privateKey, parseErr := x509.ParsePKCS1PrivateKey(privateBlock.Bytes)
		if parseErr != nil {
			return parseErr
		}
		conf.RsaKey = privateKey
	} else if os.IsNotExist(err) {
		fmt.Println("Generating new RSA key to", conf.privateKeyPath())
		reader := rand.Reader
		key, generateErr := rsa.GenerateKey(reader, rsaKeyBits)
		if generateErr != nil {
			return generateErr
		}
		conf.RsaKey = key
		conf.savePEMKey(conf.privateKeyPath(), conf.publicKeyPath())
	} else if err != nil {
		return err
	}

	return nil
}

func (conf *Config) savePEMKey(privatePath string, publicPath string) error {
	privateFile, err := os.Create(privatePath)
	if err != nil {
		return err
	}
	defer privateFile.Close()
	privateFile.Chmod(0600)

	privateKeyPem := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(conf.RsaKey),
	}

	err = pem.Encode(privateFile, privateKeyPem)
	if err != nil {
		return err
	}

	pub, err := ssh.NewPublicKey(&conf.RsaKey.PublicKey)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(publicPath, ssh.MarshalAuthorizedKey(pub), 0644)
}

func (conf *Config) initConfig() error {
	config, err := ioutil.ReadFile(conf.configPath())
	if err != nil {
		if os.IsNotExist(err) {
			if config, err = conf.downloadConfig(); err == nil {
				err = ioutil.WriteFile(conf.configPath(), config, 0664)
			}
		}
	}

	if config != nil {
		var parsed *ini.File
		parsed, err = ini.Load(config)
		if err == nil {
			conf.Connector = ConnectorConfig{
				Role:     Role(parsed.Section("").Key("role").String()),
				Resource: parsed.Section("").Key("resource").String(),
				Farm:     parsed.Section("").Key("farm").String(),
				Addr: Endpoint{
					Host: parsed.Section("").Key("host").String(),
					Port: uint16(parsed.Section("").Key("port").MustUint(0)),
				},
			}

			if conf.Connector.Addr.Host == "" {
				conf.Connector.Addr.Host = "0.0.0.0"
			}
		}
	}

	return err
}

func (conf *Config) checkJoinToken() {
	if conf.Init.Registration.JoinToken == "" {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Please paste a Join Token: ")
		joinToken, _ := reader.ReadString('\n')
		conf.Init.Registration.JoinToken = strings.Replace(joinToken, "\n", "", -1)
	}
}

func (conf *Config) downloadConfig() ([]byte, error) {
	conf.checkJoinToken()
	api, err := url.ParseRequestURI(conf.Init.Registration.Root)
	if err != nil {
		return nil, err
	}
	api.Path = conf.Init.Registration.Path

	fmt.Printf("Downloading config from %s\n", api.String())

	tr := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
		TLSClientConfig:    &tls.Config{InsecureSkipVerify: conf.Init.Registration.Insecure},
	}
	client := &http.Client{Transport: tr}

	keyFile, err := os.Open(conf.publicKeyPath())
	if err != nil {
		return nil, err
	}
	defer keyFile.Close()

	body := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(body)
	part, err := bodyWriter.CreateFormFile("key", filepath.Base(conf.publicKeyPath()))
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(part, keyFile); err != nil {
		return nil, err
	}

	bodyWriter.WriteField("token", conf.Init.Registration.JoinToken)
	if err = bodyWriter.Close(); err != nil {
		return nil, err
	}

	request, err := http.NewRequest("POST", api.String(), body)
	if err != nil {
		return nil, err
	}

	request.Header.Add("Content-Type", bodyWriter.FormDataContentType())

	response, err := client.Do(request)

	if response != nil {
		fmt.Println(response.Status)
	}

	var responseBody []byte
	if response == nil || response.StatusCode == 0 || err != nil {
		fmt.Println("Error: ", err.Error())
		fmt.Println(`Couldn't connect to Registration API. Are you using insecure tier (sandbox) ` +
			`without FROZY_INSECURE environment variable set to \"yes\"?`)
	} else if response.StatusCode != 200 {
		return nil, fmt.Errorf(response.Status)
	} else {
		defer response.Body.Close()
		if responseBody, err = ioutil.ReadAll(response.Body); err != nil {
			return nil, err
		}
	}

	return responseBody, nil
}
