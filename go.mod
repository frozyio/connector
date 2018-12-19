module gitlab.com/frozy.io/connector

require (
	github.com/coreos/etcd v3.3.11+incompatible // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/satori/go.uuid v1.2.0
	github.com/spf13/afero v1.2.0 // indirect
	github.com/spf13/cobra v0.0.3
	github.com/spf13/viper v1.3.1
	github.com/stretchr/objx v0.1.1 // indirect
	github.com/stretchr/testify v1.3.0 // indirect
	github.com/ugorji/go/codec v0.0.0-20181209151446-772ced7fd4c2 // indirect
	gitlab.com/frozy.io/connector/common v0.0.0
	golang.org/x/crypto v0.0.0-20190123085648-057139ce5d2b
	golang.org/x/sys v0.0.0-20190124100055-b90733256f2e // indirect
	gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127 // indirect
	gopkg.in/ini.v1 v1.41.0
	gopkg.in/yaml.v2 v2.2.2
)

replace gitlab.com/frozy.io/connector/common => ./common
