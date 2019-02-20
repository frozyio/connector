# Use with Docker

To launch connector interactively, simply do:

        docker run -it --rm --net=host frozy/connector

```--net=host``` is to make host ports available for a connector. In real-world
scenario you might consider opening only certain ports by using ```-p``` switch.

# Configuration file

Connector behavior can be tuned by adjusting configuration file that gets loaded
from ```$FROZY_CONFIG_DIR/connector.yaml```. ```$FROZY_CONFIG_DIR``` defaults to
```$HOME/.frozy-connector```. You could override config file path by using
```--config``` command-line argument.

For a complete configuration file reference see
[connector.template.yaml](connector.template.yaml). Each value is optional and
some values can be set via corresponding [environment variables](#environment-variables).
See descriptions below.

Minimal configuration file for use access token to register an application and make an
intent could be like that:

		frozy:
		  access_token: "ABVdVrRy3ZC5Q8b3X1K3Nsehf7EJ9yWvfLvLQRHXwec1J"

		applications:
			- name: "sql-backend.self.user-domain.sergey-afrozy-dio"
			  host: "localhost"
			  port: 5444....
		intents:.
			- src_name: "web-backend"
			  dst_name: "sql-backend.self.user-domain.sergey-afrozy-dio"
			  port: 5555..

		log:
			console:
				level: debug
				format: text

Configuration file path could be specified via command line argument:

        ./connector --config /path/to/connector.yaml

# Environment variables

Connector behavior can be tuned by adjusting environment variables. For
docker-based scenarios you do this by using ```--env``` argument for
```docker run```. Like this:

        docker run -it --rm --net=host --env FROZY_TIER=dev --env FROZY_INSECURE=yes frozy/connector

Or if you'd like to use access token to register and run new application provider:

        docker run -it --rm --net=host --env FROZY_TIER=dev --env FROZY_ACCESS_TOKEN=<access-token> \
        --env FROZY_APPLICATIONS='[{ "name": "sql_test", "host": "localhost", "port": 5544}]' frozy/connector

Or if using configuration file:


        docker run -it --rm --net=host -v </absolute/path/to/config.yaml>:/home/frozy/.frozy-connector/connector.yaml frozy/connector

Available environment (configuration) variables are:
  * ```FROZY_INSECURE``` (```frozy.insecure```) - secure mode of HTTP communications (true - http or https, false - https only) 
    with Frozy services that is used.

    Default: false (use https only)

  * ```FROZY_TIER``` (```frozy.tier```) - tier (sandbox/demo/pilot/staging) of Frozy that is used.

    Default: none (use production tier)

  * ```FROZY_CONFIG_DIR``` - Path (local to where Connector runs) to store
    connector configuration and key information

    Default: ```$HOME/.frozy-connector```

  * ```FROZY_ACCESS_TOKEN``` (```frozy.access_token```) - Access token to use
    for registration API and application owner authentication.

    Default: none (user must put access_token into configuration)

Additional environment (configuration) variables for registrations/intents applications:

  * ```FROZY_APPLICATIONS``` (```applications```) - JSON object with application information

    Default: none (primary way to configure applications is by configuration file)

    Example:
		[
		   {
			  "name": "sql_test",
			  "host": "localhost",
			  "port": 5544
		   }
		]
		where:
		<name> - application name in domain-name form (<app_name>[.<domain>]*, if <domain> is present then the name must end with application owner (which in current version has only one practical value - "self")
				 Examples: "sql", "sql.user-domain.self", "sql.user-domain.user-amail-dcom". In last example, for e-mail address string a special coding scheme is used.
		<host> - IP address or domain name of provided application
		<port> - port number of provided application

  * ```FROZY_INTENTS``` (```intents```) - JSON object with intent information

    Default: none (primary way to configure consumed applications is via configuration file)

    Example:
		[
		   {
			  "src_name": "sql_test_src",
			  "dst_name": "sql_test",
			  "port": 50544
		   },
		   {
			  "src_name": "backend_test_src",
			  "dst_name": "backend_test",
			  "port": 51644
		   }
		]
		where:
		<src_name> - source application name in domain-name form that will consume provided application pointed as <dst_name>
		<dst_name> - destination application name in domain-name form that will be consumed by application with <src_name>
		<port> - port number where connector will be listen for consumed application

		NOTE: application naming scheme is the same as ```applications``` section

		NOTE: It's possible to have both applications and intents at the same time on a given connector. It's perfrectly fine to have both ```FROZY_APPLICATIONS``` and ```FROZY_INTENTS``` defined at the same time.

# Build

        make image

# Use without Docker

To use without Docker against a sandbox tier:

        go build && FROZY_TIER=sandbox FROZY_INSECURE=yes ./connector

Optionally prepend FROZY_CONFIG_DIR to specify directory to store configs
