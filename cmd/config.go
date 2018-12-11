package cmd

import (
	"fmt"
	"os/user"
	"path"

	"github.com/spf13/viper"
	"gitlab.com/frozy.io/connector/app"
)

func getInitConfig() app.InitConfig {
	viper.SetEnvPrefix("frozy")
	viper.AllowEmptyEnv(true)
	viper.AutomaticEnv()

	configDir, err := configDir()
	if err != nil {
		fmt.Printf("Failed to get config dir: %s\n", err.Error())
	}

	return app.InitConfig{
		Registration: app.RegistrationConfig{
			Root:      configRegistrationRoot(),
			Path:      configRegistrationPath(),
			Insecure:  configRegistrationInsecure(),
			JoinToken: configJoinToken(),
		},
		Broker: app.Endpoint{
			Host: configBrokerHost(),
			Port: configBrokerPort(),
		},
		ConfigDir: configDir,
	}
}

const rootDomain = "frozy.cloud"

func configDomain() string {
	tier := viper.GetString("tier")
	var domain string
	if tier != "" {
		domain = tier + "." + rootDomain
	} else {
		domain = rootDomain
	}
	return domain
}

func configRegistrationRoot() string {
	viper.SetDefault("registration_http_schema", "https")
	registrationHTTPRoot := viper.GetString("registration_http_root")
	if registrationHTTPRoot == "" {
		registrationHTTPRoot = viper.GetString("registration_http_schema") + "://" + configDomain()
	}
	return registrationHTTPRoot
}

func configRegistrationPath() string {
	viper.SetDefault("registration_http_url", "/reg/v1/register")
	return viper.GetString("registration_http_url")
}

func configBrokerHost() string {
	brokerHost := viper.GetString("broker_host")
	if brokerHost == "" {
		brokerHost = "broker." + configDomain()
	}
	return brokerHost
}

func configBrokerPort() uint16 {
	viper.SetDefault("broker_port", 22)
	return uint16(viper.GetInt("broker_port"))
}

func configDir() (string, error) {
	configDir := viper.GetString("config_dir")
	if configDir == "" {
		cu, err := user.Current()
		if err != nil {
			return "", err
		}
		configDir = path.Join(cu.HomeDir, ".frozy-connector")
	}
	return configDir, nil
}

func configRegistrationInsecure() bool {
	insecure := viper.GetString("insecure")
	return insecure == "yes"
}

func configJoinToken() string {
	return viper.GetString("join_token")
}
