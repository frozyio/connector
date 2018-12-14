package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path"

	"github.com/spf13/viper"
	ini "gopkg.in/ini.v1"
	yaml "gopkg.in/yaml.v2"
)

// RsaKeyBits default values
const RsaKeyBits = 2048
const defaultRemoteResoursePort = 1025
const defaultBrokerPort = 22
const defaultHTTPSchema = "https"
const defaultDomain = "frozy.cloud"
const defaultRegPath = "/reg/v1/register"
const defaultAPIPath = "/api/v1"

// Role is for connector
type Role string

const (
	// Provider role id
	Provider Role = "provider"
	// Consumer role id
	Consumer Role = "consumer"
)

// Endpoint is just a pair of string and uint16
type Endpoint struct {
	Host string `yaml:",omitempty"`
	Port uint16 `yaml:",omitempty"`
}

// Network net.Addr interface
func (addr Endpoint) Network() string { return "tcp" }
func (addr Endpoint) String() string  { return fmt.Sprintf("%s:%d", addr.Host, addr.Port) }

// URLConfig .
type URLConfig struct {
	Root string `yaml:"http_root,omitempty"`
	Path string `yaml:"http_path,omitempty"`
}

// FrozyConfig is for Frozy infrastucture connection
type FrozyConfig struct {
	Tier         string    `yaml:",omitempty"`
	HTTPSchema   string    `yaml:"http_schema,omitempty"`
	Insecure     bool      `yaml:",omitempty"`
	Broker       Endpoint  `yaml:",omitempty"`
	Registration URLConfig `yaml:",omitempty"`
	API          URLConfig `yaml:",omitempty"`
}

// JoinTokenConfig .
type JoinTokenConfig struct {
	Token string
}

// AccessTokenConfig .
type AccessTokenConfig struct {
	Token        string `yaml:"access_token"`
	Resource     string
	FailIfExists bool     `yaml:"fail_if_exists,omitempty"`
	Provider     Endpoint `yaml:",omitempty"`
	Consumer     Endpoint `yaml:",omitempty"`
}

// ConnectorConfig if for configuration connection to broker
type ConnectorConfig struct {
	Role     Role
	Resource string
	Farm     string   `yaml:",omitempty"`
	Addr     Endpoint `yaml:",inline"`
}

// Config is for application initialization
type Config struct {
	dir     string            `yaml:"-"`
	Connect ConnectorConfig   `yaml:"-"`
	Frozy   FrozyConfig       `yaml:",omitempty"`
	Join    JoinTokenConfig   `yaml:",omitempty"`
	Access  AccessTokenConfig `yaml:"auto_registration,omitempty"`
}

// PrivateKeyPath .
func (c Config) PrivateKeyPath() string {
	return path.Join(c.dir, "id_rsa")
}

// PublicKeyPath .
func (c Config) PublicKeyPath() string {
	return c.PrivateKeyPath() + ".pub"
}

// RegistrationCacheFilepath is a path for connector config from registration api
func (c Config) registrationCacheFilepath() string {
	return path.Join(c.dir, "config")
}

// RemoteResourse returns addres to remote resource i.e. Farm.Resource
func (c ConnectorConfig) RemoteResourse() Endpoint {
	return Endpoint{c.Farm + "." + c.Resource, defaultRemoteResoursePort}
}

// RegistrationURL returns full base URL to frozy joint token registration API
func (c FrozyConfig) RegistrationURL() string {
	return c.buildURL(c.Registration.Root, c.Registration.Path, defaultRegPath)
}

// APIURL returns full base URL to frozy HTTP API
func (c FrozyConfig) APIURL() string {
	return c.buildURL(c.API.Root, c.API.Path, defaultAPIPath)
}

// IsConnectorConfigured returns is the connector ready to work
func (c Config) IsConnectorConfigured() bool {
	checkAddr := c.Connect.Addr.Port != 0
	if c.Connect.Role == Provider {
		checkAddr = checkAddr && c.Connect.Addr.Host != ""
	}
	return c.Connect.Role != "" && checkAddr && c.Connect.Farm != "" && c.Connect.Resource != ""
}

// IsAccessTokenConfigured returns is configuration ready for create join token
func (c Config) IsAccessTokenConfigured() bool {
	if c.Access.Token == "" {
		return false
	}

	if c.Access.Resource == "" {
		fmt.Println("You should specify frozy connector resouce name in configuration file " +
			"or set FROZY_RESOURCE_NAME environment variable " +
			"for register connector using the access token.")
		return false
	}

	isprovider := c.Access.Provider.Port != 0 && c.Access.Provider.Host != ""
	isconsumer := c.Access.Consumer.Port != 0
	if (!isprovider && !isconsumer) || (isprovider && isconsumer) {
		fmt.Println("You should specify frozy connector role in configuration file " +
			"or set FROZY_[CONSUMER|PROVIDER]_[HOST|PORT] environment variable " +
			"for register connector as consumer OR provider using the access token.")
		return false
	}

	return true
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
	return path.Join(c.dir, "connector.yaml")
}

// Load configuration
func (c *Config) Load(optionalConfig string) {
	viper.SetEnvPrefix("frozy")
	viper.AllowEmptyEnv(true)
	viper.AutomaticEnv()

	c.dir = configDir()

	if err := os.MkdirAll(c.dir, os.ModeDir|0775); err != nil && !os.IsExist(err) {
		fmt.Printf("Failed to make config directory: %s\n", err)
	}

	fmt.Println("Loading connection config cache from", c.registrationCacheFilepath())
	if bytes, err := ioutil.ReadFile(c.registrationCacheFilepath()); err == nil {
		c.UpdateCache(bytes, false)
	}

	loadPath := c.filepath()
	if optionalConfig != "" {
		loadPath = optionalConfig
	}

	fmt.Println("Loading config from", loadPath)
	if bs, err := ioutil.ReadFile(loadPath); err == nil {
		yaml.Unmarshal(bs, c)
	} else {
		fmt.Printf("Warning: %s. Using FROZY_* env variables.\n", err)
	}

	c.enrichFromEnv()
}

// UpdateCache updates configuration with registration reply
func (c *Config) UpdateCache(data []byte, save bool) bool {
	if parsed, err := ini.Load(data); err != nil {
		fmt.Printf("Failed to parse registration config: %s\n", err.Error())
	} else {
		c.Connect = ConnectorConfig{
			Role:     Role(parsed.Section("").Key("role").String()),
			Resource: parsed.Section("").Key("resource").String(),
			Farm:     parsed.Section("").Key("farm").String(),
			Addr: Endpoint{
				Host: parsed.Section("").Key("host").String(),
				Port: uint16(parsed.Section("").Key("port").MustUint(0)),
			},
		}

		if c.Connect.Addr.Host == "" {
			c.Connect.Addr.Host = "0.0.0.0"
		}
	}

	if c.IsConnectorConfigured() {
		if save {
			fmt.Println("Saving connection config cache to", c.registrationCacheFilepath())
			if err := ioutil.WriteFile(c.registrationCacheFilepath(), data, 0644); err != nil {
				fmt.Printf("Failed to write config cache to %s. Error: %s.\n",
					c.registrationCacheFilepath(), err.Error())
			}
		}
		return true
	}

	return false
}

func (c Config) String() string {
	bs, _ := yaml.Marshal(c)
	return string(bs)
}

func (c Config) Write() error {
	bs, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	fmt.Println("Saving config to", c.filepath())
	return ioutil.WriteFile(c.filepath(), bs, 0644)
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
	checkSetString("tier", &c.Frozy.Tier)
	checkSetString("registration_http_schema", &c.Frozy.HTTPSchema)
	checkSetBool("insecure", &c.Frozy.Insecure)
	checkSetString("registration_http_root", &c.Frozy.Registration.Root)
	checkSetString("registration_http_url", &c.Frozy.Registration.Path)
	checkSetString("broker_host", &c.Frozy.Broker.Host)
	checkSetUInt16("broker_port", &c.Frozy.Broker.Port)
	checkSetString("backend_url", &c.Frozy.API.Root)

	checkSetString("join_token", &c.Join.Token)

	checkSetString("resource_name", &c.Access.Resource)
	checkSetString("access_token", &c.Access.Token)
	if viper.IsSet("consumer_port") {
		c.Access.Consumer.Port = uint16(viper.GetInt("consumer_port"))
	}
	if viper.IsSet("provider_port") {
		c.Access.Provider.Port = uint16(viper.GetInt("provider_port"))
		checkSetString("provider_host", &c.Access.Provider.Host)
	}
	checkSetBool("fail_if_exists", &c.Access.FailIfExists)
}

func currentUserHomeDir() string {
	user, err := user.Current()
	if err == nil {
		return user.HomeDir
	}
	return os.Getenv("HOME")
}

func configDir() string {
	viper.SetDefault("config_dir", path.Join(currentUserHomeDir(), ".frozy-connector"))
	return viper.GetString("config_dir")
}
