package config

import (
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path"
	"reflect"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	yaml "gopkg.in/yaml.v2"
)

const (
	// RsaKeyBits default values
	RsaKeyBits                            = 2048
	defaultBrokerPort                     = 22
	defaultHTTPSchema                     = "https"
	defaultDomain                         = "frozy.cloud"
	defaultRegPath                        = "/reg/v1/register"
	defaultBrDiscPath                     = "/"
	defaultRegistrationAccessTokenName    = "access-token"
	defaultBrokerDiscoveryAccessTokenName = "X-Access-Token"
)

// ProvideAppInfo is provider application specific information
type ProvideAppInfo struct {
	Name string `mapstructure:"name" json:"name" yaml:"name"`
	Host string `mapstructure:"host" json:"host" yaml:"host"`
	Port uint16 `mapstructure:"port" json:"port" yaml:"port"`
}

// IntentAppInfo is intent application specific information
type IntentAppInfo struct {
	SrcName string `mapstructure:"src_name" json:"src_name" yaml:"src_name"`
	DstName string `mapstructure:"dst_name" json:"dst_name" yaml:"dst_name"`
	Port    uint16 `mapstructure:"port" json:"port" yaml:"port"`
}

// Endpoint is just a pair of string and uint16
type Endpoint struct {
	Host string `mapstructure:"host" yaml:"host"`
	Port uint16 `mapstructure:"port" yaml:"port"`
}

// Network net.Addr interface
func (addr Endpoint) Network() string { return "tcp" }
func (addr Endpoint) String() string  { return fmt.Sprintf("%s:%d", addr.Host, addr.Port) }

// URLConfig .
type URLConfig struct {
	AccessTokenName string `mapstructure:"http_access_token_name" yaml:"http_access_token_name"`
	Root            string `mapstructure:"http_root" yaml:"http_root"`
	Path            string `mapstructure:"http_path" yaml:"http_path"`
}

// FrozyConfig is for Frozy infrastucture connection
type FrozyConfig struct {
	ConnectorName   string      `mapstructure:"name" yaml:"name"`
	AccessToken     RemoteValue `mapstructure:"access_token" yaml:"access_token"`
	Tier            RemoteValue `mapstructure:"tier" yaml:"tier"`
	HTTPSchema      string      `mapstructure:"http_schema" yaml:"http_schema"`
	Insecure        bool        `mapstructure:"insecure" yaml:"insecure"`
	BrokerStatic    Endpoint    `mapstructure:"broker" yaml:"broker"`
	BrokerDiscovery URLConfig   `mapstructure:"broker_discovery" yaml:"broker_discovery"`
	Registration    URLConfig   `mapstructure:"registration" yaml:"registration"`
}

// LogConfig holds information about possible loggers
type LogConfig struct {
	Console LogConsoleConfig `mapstructure:"console" yaml:"console"`
	File    LogFileConfig    `mapstructure:"file" yaml:"file"`
}

// LogConsoleConfig describes console logging config
type LogConsoleConfig struct {
	Level  string `mapstructure:"level" yaml:"level"`
	Format string `mapstructure:"format" yaml:"format"`
	Color  bool   `mapstructure:"color" yaml:"color"`
}

// LogFileConfig describes file logging config
type LogFileConfig struct {
	Level    string `mapstructure:"level" yaml:"level"`
	Format   string `mapstructure:"format" yaml:"format"`
	FilePath string `mapstructure:"path" yaml:"path"`
}

// Config is for application initialization
type Config struct {
	Frozy        FrozyConfig      `mapstructure:"frozy" yaml:"frozy"`
	Applications []ProvideAppInfo `mapstructure:"applications" yaml:"applications"`
	Intents      []IntentAppInfo  `mapstructure:"intents" yaml:"intents"`
	Log          LogConfig        `mapstructure:"log" yaml:"log"`
}

var workDirectory string

// RegistrationAccessTokenName
func (c Config) RegistrationAccessTokenName() string {
	if len(c.Frozy.Registration.AccessTokenName) == 0 {
		return defaultRegistrationAccessTokenName
	}

	return c.Frozy.Registration.AccessTokenName
}

// BrokerDiscoveryAccessTokenName
func (c Config) BrokerDiscoveryAccessTokenName() string {
	if len(c.Frozy.BrokerDiscovery.AccessTokenName) == 0 {
		return defaultRegistrationAccessTokenName
	}

	return c.Frozy.BrokerDiscovery.AccessTokenName
}

// PrivateKeyPath .
func (c Config) PrivateKeyPath() string {
	return path.Join(workDirectory, "id_rsa")
}

// PublicKeyPath .
func (c Config) PublicKeyPath() string {
	return c.PrivateKeyPath() + ".pub"
}

// RegistrationURL returns full base URL to frozy join token registration API
func (c FrozyConfig) RegistrationURL() (string, error) {
	return c.buildURL(c.Registration.Root, c.Registration.Path, defaultRegPath)
}

// BrokerDiscoveryURL returns full base URL to frozy broker discovery service
func (c FrozyConfig) BrokerDiscoveryURL() (string, error) {
	return c.buildURL(c.BrokerDiscovery.Root, c.BrokerDiscovery.Path, defaultBrDiscPath)
}

// BrokerAddr .
func (c FrozyConfig) BrokerAddr() (Endpoint, error) {
	rv := c.BrokerStatic
	if rv.Host == "" {
		tier, err := c.tier()
		if err != nil {
			return Endpoint{}, err
		}
		rv.Host = fmt.Sprintf("broker.%s%s", tier, defaultDomain)
	}
	if rv.Port == 0 {
		rv.Port = defaultBrokerPort
	}
	return rv, nil
}

func (c FrozyConfig) tier() (string, error) {
	bytes, err := c.Tier.Value()
	if err != nil {
		return "", err
	}
	value := string(bytes)

	if value != "" {
		return value + ".", nil
	}
	return "", nil
}

func (c FrozyConfig) buildURL(root, path, defaultPath string) (string, error) {
	if root == "" {
		schema := c.HTTPSchema
		if schema == "" {
			schema = defaultHTTPSchema
		}
		tier, err := c.tier()
		if err != nil {
			return "", err
		}
		root = fmt.Sprintf("%s://%s%s", schema, tier, defaultDomain)
	}
	if path == "" {
		path = defaultPath
	}
	return root + path, nil
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
		fmt.Printf("Failed to make config directory: [%s] due to: %v\n", workDirectory, err)
	}

	// build full config path
	loadPath := c.filepath()
	if optionalConfig != "" {
		loadPath = optionalConfig
	}

	fmt.Printf("Loading config from: %s\n", loadPath)
	viper.SetConfigFile(loadPath)
	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("Failed to read config due to: %v, trying to get configuration from environment variables\n", err)
	} else {
		opts := viper.DecodeHook(DecodeHookRemoteValue)
		err = viper.UnmarshalKey("frozy", &c.Frozy, opts)
		if err != nil {
			fmt.Printf("Failed to Unmarshal frozy part of config due to: %v\n", err)
		}

		err = viper.UnmarshalKey("applications", &c.Applications, opts)
		if err != nil {
			fmt.Printf("Failed to Unmarshal applications part of config due to: %v\n", err)
		}

		err = viper.UnmarshalKey("intents", &c.Intents, opts)
		if err != nil {
			fmt.Printf("Failed to unmarshal intents part of config due to: %v\n", err)
		}

		err = viper.UnmarshalKey("log", &c.Log, opts)
		if err != nil {
			fmt.Printf("Failed to Unmarshal console log part of config due to: %v\n", err)
		}
	}

	c.enrichFromEnv()
}

// ResolveRemoteValues attempts to resolve all remote values present in
// configuration
func (c *Config) ResolveRemoteValues() error {
	err := c.Frozy.Tier.Resolve()
	if err != nil {
		return err
	}

	err = c.Frozy.AccessToken.Resolve()
	return err
}

// ResolveRemoteValuesRetryIntervalSeconds is amount of seconds to wait before
// trying to resolve configuration remote values one more time.
const ResolveRemoteValuesRetryIntervalSeconds = 5

// ResolveRemoteValuesUntilSuccess calls ResolveSecrets as many times as required to
// finally achieve success.
func (c *Config) ResolveRemoteValuesUntilSuccess(logger *log.Entry) {
	for {
		err := c.ResolveRemoteValues()
		if err == nil {
			return
		}

		logger.Warnf("Failed to resolve remote values due to: %v. Retrying in %d seconds", err, ResolveRemoteValuesRetryIntervalSeconds)
		time.Sleep(ResolveRemoteValuesRetryIntervalSeconds * time.Second)
	}
}

func checkSetString(key string, out *string) {
	if viper.IsSet(key) {
		*out = viper.GetString(key)
	}
}

func checkSetRemoteValue(key string, out *RemoteValue) {
	if viper.IsSet(key) {
		*out = LiteralString(viper.GetString(key))
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
	checkSetRemoteValue("access_token", &c.Frozy.AccessToken)
	checkSetRemoteValue("tier", &c.Frozy.Tier)

	// we will use FROZY_APPLICATIONS/FROZY_INTENTS environments with JSON data
	var applications, intents string
	checkSetString("applications", &applications)
	checkSetString("intents", &intents)

	// for applications we will check if applications already registered from config file
	if len(applications) != 0 {
		// define search helper
		checkIfAppExistsAndAdd := func(data *ProvideAppInfo) {
			for _, appDataVal := range c.Applications {
				if reflect.DeepEqual(&appDataVal, data) {
					return
				}
			}
			c.Applications = append(c.Applications, *data)
		}

		var pApps []ProvideAppInfo
		err := json.Unmarshal([]byte(applications), &pApps)
		if err != nil {
			fmt.Printf("Can't Unmarshal register application config data: [%v] received from ENV due to: %v\n", applications, err)
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
			for _, appDataVal := range c.Intents {
				if reflect.DeepEqual(&appDataVal, data) {
					return
				}
			}
			c.Intents = append(c.Intents, *data)
		}

		var cApps []IntentAppInfo
		err := json.Unmarshal([]byte(intents), &cApps)
		if err != nil {
			fmt.Printf("Can't Unmarshal intents application config data: [%s] received from ENV due to: %v\n", intents, err)
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
