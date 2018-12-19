# Use with Docker

First of all you need to build image (that's until we have an organization at
Docker Hub). See below for build instructions.

Once you have an image built you could do

        docker run -it --rm -p <host-port>:<container-port> frozy/connector

```<container-port>``` should be what you have entered to frontend when obtaining join
token. ```<host-port>``` is whatever port you'd like to access your application at.

On provider side things get slightly tricky because container by default cannot
access host ports (that's not an issue with stuff like Kubernetes pods where
containers run side-by-side, but for simple hands-on experience with running
docker manually on Linux host this can become frustrating).

A simple (although arguably insecure) way to just get things done is to start
connector on provider side with --net=host argument like this:

        docker run -it --rm --net=host frozy/connector

# Configuration file

Connector behavior can be tuned by adjusting configuration file that gets loaded
from ```$FROZY_CONFIG_DIR/connector.yaml```. ```$FROZY_CONFIG_DIR``` defaults to
```$HOME/.frozy-connector```. You could override config file path by using
```--config``` command-line argument.

All possible configuration file fields are demonstrated in
[connector.template.yaml](connector.template.yaml). Each value is optional and
can be replaced/set by a corresponding [environment variable](#environment-variables)
if one exists. See descriptions below.

When connector starts it checks if connector registration reply cache exsits in
```$FROZY_CONFIG_DIR/config``` . If yes, then it immediately connects to broker
and resumes what it done previously. Otherwise it checks ```auto_registration``` 
section of YAML configuration.

If `access_token` section exists then it performs key registration action and 
connects to broker.

Minimal configuration file for use access token to register new resource and make provider
could be like that:

        auto_registration:
          access_token: "qwerty12345"
          
        applications-config:
          applications:
            - name: "sql"
              access_token: "ABVdVrRy3ZC5Q8vfLvLQRHXwec1J"
		      host: "localhost"
              port: 5444
              
              AND/OR
              
          intents:
            - src_name: "web-backend"
			  dst_name: "sql-backend.self.user-domain.username-afrozy-dio"
			  access_token: "ABVdVrRy3ZC5Q8vfLvLQRHXwec1J"
			  port: 5555  

Configuration file path could be specified via command line argument:

        ./connector --config /path/to/connector.yaml

In such case configuration will be read from the specified file.

# Environment variables

Connector behavior can be tuned by adjusting environment variables. For
docker-based scenarios you do this by using ```--env``` argument for
```docker run```. Like this:

        docker run -it --rm --net=host --env FROZY_TIER=sandbox --env FROZY_INSECURE=yes frozy/connector

Or if you'd like to use access token to register and run new application provider:	

        docker run -it --rm --net=host --env FROZY_TIER=sandbox --env FROZY_ACCESS_TOKEN=<access-token> --env FROZY_APPLICATIONS='[{ "name": "sql_test", "access_token": "ABVdVrRy3ZC5", "host": "localhost", "port": 5544}]' frozy/connector

Or if using configuration file:

        docker run -it --rm --net=host --mount type=bind,source=/dir/with/config,target=/etc/frozy-connector frozy/connector --config /etc/frozy-connector/connector.yaml

Available environment (configuration) variables are:
  * ```FROZY_TIER``` (```frozy.tier```) - tier (sandbox/demo/pilot/staging) of Frozy that is used.

    Default: none (use production tier)

  * ```FROZY_INSECURE``` (```frozy.insecure```) - If set to 'yes' then ignore TLS certificate validation
    errors when accessing Registration API.

    Default: none (validate TLS certificate)

  * ```FROZY_BROKER_HOST``` (```frozy.broker.host```) - hostname of the Frozy Broker to connect to.

    Default (if ```FROZY_TIER``` is set): ```broker.$FROZY_TIER.frozy.cloud```

    Default (if ```FROZY_TIER``` is not set): ```broker.frozy.cloud```

  * ```FROZY_BROKER_PORT``` (```frozy.broker.port```) - Broker TCP port number

    Default: 2200

  * ```FROZY_REGISTRATION_HTTP_SCHEMA``` (```frozy.http_schema```) - URL schema (http/https) that would be
    used be used to access Registration API Server.

    Default: ```https```

  * ```FROZY_REGISTRATION_HTTP_ROOT``` (```frozy.registration.http_root```) - Root URL of registration API server.

    Default (if ```FROZY_TIER``` is set): ```$FROZY_TIER.frozy.cloud```

    Default (if ```FROZY_TIER``` is not set): ```frozy.cloud```

  * ```FROZY_REGISTRATION_HTTP_URL``` (```frozy.registration.http_path```) - Path to ```register``` method of Registration
    API under ```$FROZY_REGISTRATION_HTTP_ROOT```

    Default: ```/reg/v1/register```

  * ```FROZY_CONFIG_DIR``` - Path (local to where Connector runs) to store
    connector configuration and key information

    Default: ```$HOME/.frozy-connector```

  * ```FROZY_ACCESS_TOKEN``` (```auto_registration.access_token```) - Access
    token to use for registration API authentication.

    Default: none (user must point access_token for key registration process)

Additional environment (configuration) variables for provide/consume applications:

  * ```FROZY_APPLICATIONS``` (```applications-config.applications```) - JSON object with provided applications information
  
    Default: none (primary way to configure provided applications is via configuration file)
    
    Example: 
		[
		   {
			  "name": "sql_test",
			  "access_token": "ABVdVrRy3ZC5Q8b3X1K3Nsehf7",
			  "host": "localhost",
			  "port": 5544
		   }
		]
		where:
		<name> - application name in domain-name form (<app_name>[.<domain>]*, if <domain> is used in application naming scheme then user`s e-mail 
				 or special reserved word <self> must pointed at the end of name). 
				 Examples: "sql", "sql.user-domain.self", "sql.user-domain.user-amail-dcom". In last example, for e-mail address string a special coding scheme is used.
		<access_token> - user`s access_token that granted with rights to provide pointed application
		<host> - IP address or domain name of provided application
		<port> - port number of provided application
    
  * ```FROZY_INTENTS``` (```applications-config.intents```) - JSON object with consumed applications information
  
    Default: none (primary way to configure consumed applications is via configuration file)
    
    Example:
		[
		   {
			  "src_name": "sql_test_src",
			  "dst_name": "sql_test",
			  "access_token": "ABVdVrRy3ZC5Q",      
			  "port": 50544
		   },
		   {
			  "src_name": "backend_test_src",
			  "dst_name": "backend_test",
			  "access_token": "ABVdVrRy3ZC5QJ",
			  "port": 51644
		   }
		]
		where:
		<src_name> - source application name in domain-name form that will consume provided application pointed as <dst_name>
		<dst_name> - destination application name in domain-name form that will be consumed by application with <src_name>
					NOTE: application naming scheme same as for PROVIDED application <name> field
		<access_token> - user`s access_token that granted with rights to consume provided application
		<port> - port number where connector will be listen for consumed application
    
  NOTE: The connector can be used for providing of applications and their consumption in same time, so both sections of the configuration can be filled

    
# Build

        make image

# Use without Docker

To use without Docker against a sandbox tier:

        FROZY_TIER=sandbox FROZY_INSECURE=yes ./connector

Optionally prepend FROZY_CONFIG_DIR to specify directory to store configs
