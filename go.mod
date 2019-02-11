module gitlab.com/frozy.io/connector

require (
	cloud.google.com/go v0.35.1
	github.com/coreos/etcd v3.3.11+incompatible // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/mitchellh/mapstructure v1.1.2
	github.com/satori/go.uuid v1.2.0
	github.com/sirupsen/logrus v1.3.0
	github.com/spf13/cobra v0.0.3
	github.com/spf13/viper v1.3.1
	github.com/ugorji/go/codec v0.0.0-20181209151446-772ced7fd4c2 // indirect
	gitlab.com/frozy.io/connector/common v0.0.0
	gitlab.com/frozy.io/connector/logconf v0.0.0
	golang.org/x/crypto v0.0.0-20190123085648-057139ce5d2b
	google.golang.org/genproto v0.0.0-20190123001331-8819c946db44
	gopkg.in/ini.v1 v1.41.0
	gopkg.in/yaml.v2 v2.2.2
)

replace gitlab.com/frozy.io/connector/common => ./common

replace gitlab.com/frozy.io/connector/logconf => ./logconf
