package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path"
	"reflect"

	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v2"
)

// RsaKeyBits default values
const RsaKeyBits = 2048
const defaultBrokerPort = 2200
const defaultHTTPSchema = "https"
const defaultDomain = "frozy.cloud"
const defaultRegPath = "/reg/v1/register"
const defaultAPIPath = "/api/v1"

// ProovideAppInfo is provider application specific information
type ProovideAppInfo struct {
	Name        string `mapstructure:"name" json:"name"`
	AccessToken string `mapstructure:"access_token" json:"access_token"`
	Host        string `mapstructure:"host" json:"host"`
	Port        uint16 `mapstructure:"port" json:"port"`
}

// IntentAppInfo is intent application specific information
type IntentAppInfo struct {
	SrcName     string `mapstructure:"src_name" json:"src_name"`
	DstName     string `mapstructure:"dst_name" json:"dst_name"`
	AccessToken string `mapstructure:"access_token" json:"access_token"`
	Port        uint16 `mapstructure:"port" json:"port"`
}

// Endpoint is just a pair of string and uint16
type Endpoint struct {
	Host string `mapstructure:"host"`
	Port uint16 `mapstructure:"port"`
}

// Network net.Addr interface
func (addr Endpoint) Network() string { return "tcp" }
func (addr Endpoint) String() string  { return fmt.Sprintf("%s:%d", addr.Host, addr.Port) }

// URLConfig .
type URLConfig struct {
	Root string `mapstructure:"http_root"`
	Path string `mapstructure:"http_path"`
}

// FrozyConfig is for Frozy infrastucture connection
type FrozyConfig struct {
	ConnectorName string    `mapstructure:"name"`
	AccessToken   string    `mapstructure:"access_token"`
	Tier          string    `mapstructure:"tier"`
	HTTPSchema    string    `mapstructure:"http_schema"`
	Insecure      bool      `mapstructure:"insecure"`
	Broker        Endpoint  `mapstructure:"broker"`
	Registration  URLConfig `mapstructure:"registration"`
}

type ApplicationsConfig struct {
	Applications []ProovideAppInfo `mapstructure:"applications"`
	Intents      []IntentAppInfo   `mapstructure:"intents"`
}

// Config is for application initialization
type Config struct {
	Frozy        FrozyConfig        `mapstructure:"frozy"`
	Applications ApplicationsConfig `mapstructure:"applications-config"`
}

var workDirectory string

// PrivateKeyPath .
func (c Config) PrivateKeyPath() string {
	return path.Join(workDirectory, "id_rsa")
}

// PublicKeyPath .
func (c Config) PublicKeyPath() string {
	return c.PrivateKeyPath() + ".pub"
}

// RegistrationURL returns full base URL to frozy joint token registration API
func (c FrozyConfig) RegistrationURL() string {
	return c.buildURL(c.Registration.Root, c.Registration.Path, defaultRegPath)
}

// BrokerAddr .
func (c FrozyConfig) BrokerAddr() Endpoint {
	rv := c.Broker
	if rv.Host == "" {
		rv.Host = fmt.Sprintf("broker.%s%s", c.tier(), defaultDomain)
	}
	if rv.Port == 0 {
		rv.Port = defaultBrokerPort
	}
	return rv
}

func (c FrozyConfig) tier() string {
	if c.Tier != "" {
		return c.Tier + "."
	}
	return ""
}

func (c FrozyConfig) buildURL(root, path, defaultPath string) string {
	if root == "" {
		schema := c.HTTPSchema
		if schema == "" {
			schema = defaultHTTPSchema
		}
		root = fmt.Sprintf("%s://%s%s", schema, c.tier(), defaultDomain)
	}
	if path == "" {
		path = defaultPath
	}
	return root + path
}

func (c Config) filepath() string {
	return path.Join(workDirectory, "connector.yaml")
}

func (c Config) String() string {
	bs, _ := yaml.Marshal(c)
	return string(bs)
}

// Load configuration
func (c *Config) Load(optionalConfig string) {
	viper.SetEnvPrefix("frozy")
	viper.AllowEmptyEnv(true)
	viper.AutomaticEnv()

	// chek if config directory exists and create it if not
	workDirectory = configDir()
	if err := os.MkdirAll(workDirectory, os.ModeDir|0775); err != nil && !os.IsExist(err) {
		fmt.Printf("Failed to make config directory: %s\n", err)
	}

	// build full config path
	loadPath := c.filepath()
	if optionalConfig != "" {
		loadPath = optionalConfig
	}

	fmt.Println("Loading config from", loadPath)
	viper.SetConfigFile(loadPath)
	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("Failed to read config (%v), trying to get configuration from environment variables\n", err)
	} else {
		err = viper.UnmarshalKey("frozy", &c.Frozy)
		if err != nil {
			fmt.Printf("Failed to Unmarshal frozy part of config (%v)\n", err)
		}

		err = viper.UnmarshalKey("applications-config", &c.Applications)
		if err != nil {
			fmt.Printf("Failed to Unmarshal application part of config (%v)\n", err)
		}
	}

	c.enrichFromEnv()
}

func checkSetString(key string, out *string) {
	if viper.IsSet(key) {
		*out = viper.GetString(key)
	}
}

func checkSetUInt16(key string, out *uint16) {
	if viper.IsSet(key) {
		*out = uint16(viper.GetInt(key))
	}
}

func checkSetBool(key string, out *bool) {
	if viper.IsSet(key) {
		*out = viper.GetString(key) == "yes"
	}
}

func (c *Config) enrichFromEnv() {
	checkSetString("access_token", &c.Frozy.AccessToken)
	checkSetString("tier", &c.Frozy.Tier)
	checkSetString("registration_http_schema", &c.Frozy.HTTPSchema)
	checkSetBool("insecure", &c.Frozy.Insecure)
	checkSetString("broker_host", &c.Frozy.Broker.Host)
	checkSetUInt16("broker_port", &c.Frozy.Broker.Port)
	checkSetString("registration_http_root", &c.Frozy.Registration.Root)
	checkSetString("registration_http_url", &c.Frozy.Registration.Path)

	// we will use FROZY_APPLICATIONS/FROZY_INTENTS environments with JSON data
	var applications, intents string
	checkSetString("applications", &applications)
	checkSetString("intents", &intents)

	// for applications we will check if applications already registered from config file
	if len(applications) != 0 {
		// define search helper
		checkIfAppExistsAndAdd := func(data *ProovideAppInfo) {
			for _, appDataVal := range c.Applications.Applications {
				if reflect.DeepEqual(&appDataVal, data) {
					return
				}
			}
			c.Applications.Applications = append(c.Applications.Applications, *data)
		}

		var pApps []ProovideAppInfo
		err := json.Unmarshal([]byte(applications), &pApps)
		if err != nil {
			fmt.Printf("Can't Unmarshal provide application config data received from ENV due to: (%v)\n", err)
		} else {
			// do processing
			for _, appData := range pApps {
				checkIfAppExistsAndAdd(&appData)
			}
		}
	}

	if len(intents) != 0 {
		// define search helper
		checkIfAppExistsAndAdd := func(data *IntentAppInfo) {
			for _, appDataVal := range c.Applications.Intents {
				if reflect.DeepEqual(&appDataVal, data) {
					return
				}
			}
			c.Applications.Intents = append(c.Applications.Intents, *data)
		}

		var cApps []IntentAppInfo
		err := json.Unmarshal([]byte(intents), &cApps)
		if err != nil {
			fmt.Printf("Can't Unmarshal intents application config data received from ENV due to: (%v)\n", err)
		} else {
			// do processing
			for _, appData := range cApps {
				checkIfAppExistsAndAdd(&appData)
			}
		}
	}
}

func currentUserHomeDir() string {
	user, err := user.Current()
	if err == nil {
		return user.HomeDir
	}
	return os.Getenv("HOME")
}

func configDir() string {
	// get config dir from enviroments or default value if environment is not set
	viper.SetDefault("config_dir", path.Join(currentUserHomeDir(), ".frozy-connector"))
	return viper.GetString("config_dir")
}
