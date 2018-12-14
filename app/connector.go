package app

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/frozy.io/connector/config"
	"golang.org/x/crypto/ssh"
)

// Connector .
type Connector struct {
	Config config.Config
	RsaKey *rsa.PrivateKey
}

// Execute the connectioor
func Execute(optionalConfig string) error {
	var connector Connector

	connector.Config.Load(optionalConfig)
	fmt.Printf("Initializing with:\n%s\n", connector.Config.String())

	if err := connector.initialize(); err != nil {
		return err
	}

	connector.Config.Write()

	return connector.run()
}

func (c *Connector) initialize() error {
	// Initialize RSA identity key
	if err := c.initIdentity(); err != nil {
		return err
	}

	if c.Config.IsConnectorConfigured() {
		return nil
	}

	if c.Config.Join.Token == "" {
		// Obtain join token using access token or from stdin

		var err error
		if c.Config.Access.Token != "" {
			if !c.Config.IsAccessTokenConfigured() {
				return fmt.Errorf("Invalid connector configuraiotn")
			}
			if c.Config.Access.Provider.Host == "" && c.Config.Access.Provider.Port != 0 {
				c.Config.Access.Provider.Host = "0.0.0.0"
			}
			err = c.obtainJoinTokenFromAPI()
		} else {
			err = c.readJoinTokenFromStdin()
		}

		if err != nil {
			return err
		}
	}

	return c.registerJoinToken()
}

func (c *Connector) run() error {
	var err error
	switch c.Config.Connect.Role {
	case config.Consumer:
		err = c.runConsumer()
	case config.Provider:
		err = c.runProvider()
	default:
		err = fmt.Errorf("Invalid connector role %s", c.Config.Connect.Role)
	}
	return err
}

func (c *Connector) initIdentity() error {
	if privatePem, err := ioutil.ReadFile(c.Config.PrivateKeyPath()); err == nil {
		fmt.Println("Reading RSA key from", c.Config.PrivateKeyPath())
		privateBlock, _ := pem.Decode(privatePem)
		privateKey, parseErr := x509.ParsePKCS1PrivateKey(privateBlock.Bytes)
		if parseErr != nil {
			return parseErr
		}
		c.RsaKey = privateKey
	} else if os.IsNotExist(err) {
		fmt.Println("Generating new RSA key to", c.Config.PrivateKeyPath())
		reader := rand.Reader
		key, generateErr := rsa.GenerateKey(reader, config.RsaKeyBits)
		if generateErr != nil {
			return generateErr
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

func (c *Connector) registerJoinToken() error {
	data, err := c.downloadConfig()
	if err != nil {
		return err
	}

	if !c.Config.UpdateCache(data, true) {
		return fmt.Errorf("Failed to configure connector with registration API response")
	}

	return nil
}

func (c *Connector) readJoinTokenFromStdin() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Please paste a Join Token: ")
	joinToken, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	c.Config.Join.Token = strings.Replace(joinToken, "\n", "", -1)
	return nil
}

func (c *Connector) obtainJoinTokenFromAPI() error {
	client := c.httpClient()

	doRequest := func(meth, url string, data []byte) (int, []byte, error) {
		request, err := http.NewRequest(meth, url, bytes.NewReader(data))
		if err != nil {
			return 0, nil, err
		}
		request.Header.Add("X-Access-Token", c.Config.Access.Token)
		if err != nil {
			return 0, nil, err
		}
		response, err := client.Do(request)
		if response != nil && response.StatusCode == 409 {
			if !c.Config.Access.FailIfExists {
				response.StatusCode = 200
			} else {
				return response.StatusCode, nil,
					fmt.Errorf("Resource %s already exists", c.Config.Access.Resource)
			}
		}

		return checkHTTPResponse(response, err)
	}

	type apiinfo struct {
		Email  string
		FarmID string
	}

	infoURL := c.Config.Frozy.APIURL() + "/info"
	fmt.Println("Request farm ID url:", infoURL)
	_, response, err := doRequest("GET", infoURL, nil)
	if err != nil {
		return err
	}
	var info apiinfo
	if err = json.Unmarshal(response, &info); err != nil {
		return err
	}
	fmt.Println("FarmId:", info.FarmID)

	resourceURL := fmt.Sprintf("%s/farm/%s/resources/%s",
		c.Config.Frozy.APIURL(), info.FarmID, c.Config.Access.Resource)
	fmt.Println("Create resource url:  ", resourceURL)
	if _, _, err = doRequest("PUT", resourceURL, nil); err != nil {
		return err
	}

	tokenURL := resourceURL + "/tokens"
	fmt.Println("Create join token url:", tokenURL)
	var payload string
	if c.Config.Access.Consumer.Port != 0 {
		payload = fmt.Sprintf(`{"role":"%s","port":"%d"}`,
			config.Consumer, c.Config.Access.Consumer.Port)
	} else {
		payload = fmt.Sprintf(`{"role":"%s","port":"%d","host":"%s"}`,
			config.Provider, c.Config.Access.Provider.Port, c.Config.Access.Provider.Host)
	}
	fmt.Println(payload)
	_, response, err = doRequest("POST", tokenURL, []byte(payload))
	if err != nil {
		return err
	}
	if response == nil || len(response) == 0 {
		return fmt.Errorf("Invalid create join token response")
	}

	c.Config.Join.Token = string(response)
	return nil
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

func (c *Connector) downloadConfig() ([]byte, error) {
	keyFile, err := os.Open(c.Config.PublicKeyPath())
	if err != nil {
		return nil, err
	}
	defer keyFile.Close()

	body := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(body)
	part, err := bodyWriter.CreateFormFile("key", filepath.Base(c.Config.PublicKeyPath()))
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(part, keyFile); err != nil {
		return nil, err
	}

	bodyWriter.WriteField("token", c.Config.Join.Token)
	if err = bodyWriter.Close(); err != nil {
		return nil, err
	}

	api := c.Config.Frozy.RegistrationURL()
	fmt.Printf("Downloading config from %s\n", api)

	request, err := http.NewRequest("POST", api, body)
	if err != nil {
		return nil, err
	}

	request.Header.Add("Content-Type", bodyWriter.FormDataContentType())
	response, err := c.httpClient().Do(request)
	_, responseBody, err := checkHTTPResponse(response, err)
	return responseBody, err
}

func checkHTTPResponse(response *http.Response, err error) (int, []byte, error) {
	if response != nil {
		fmt.Println("API HTTP response:", response.Status)
	}

	if response == nil || response.StatusCode == 0 || err != nil {
		fmt.Println("Couldn't connect to Registration API. Are you using insecure tier (sandbox) " +
			`without FROZY_INSECURE environment variable set to "yes"?`)
		if err != nil {
			fmt.Println("Error: ", err.Error())
			return 0, nil, err
		}
		return 0, nil, fmt.Errorf(response.Status)
	}

	if response.StatusCode != 200 {
		if response.StatusCode == 401 {
			fmt.Println("Most likely access Token is not valid. Try obtaining it from the frontend " +
				"once again.")
		}
		return 0, nil, fmt.Errorf(response.Status)
	}

	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return 0, nil, err
	}
	fmt.Println(string(body))

	return response.StatusCode, body, nil
}
