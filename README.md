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
```$FROZY_CONFIG_DIR/config``` . If yes, then it immediately connects to the broker.
Else it read `.yaml` configuration and if `join` section exists then it perform
registration action, dump reply cache and then connect to broker with obtained connector values.

In other case if `auto_registration` section contains exists it registers a
resource and obtains join token, register it and connect to broker.

And finally if registration reply cache not exists, `join` and `auto_registration` sections
not provided then it ask join token from stdin, register in and connect to the broker.

So minimal configuration file for use access token to register new resource and make provider
could be like that:

auto_registration:
  access_token: qwerty12345
  resource: ResourceName
  provider:
    host: localhost
    port: 1234

Configuration file could be accepted by command line args:

    ./connector --config /path/to/connector.yaml

In such case configuration will be read from the specified file, processed and write to
the default location.

# Environment variables

Connector behavior can be tuned by adjusting environment variables. For
docker-based scenarios you do this by using ```--env``` argument for ```docker run```.
Like this:

        docker run -it --rm --net=host --env FROZY_TIER=sandbox --env FROZY_INSECURE=yes frozy/connector

Or using access token to register and run new provider:

        docker run -it --rm --net=host --env FROZY_TIER=sandbox --env FROZY_ACCESS_TOKEN=<access-token> --env FROZY_RESOURCE_NAME=Cat --env FROZY_PROVIDER_HOST=localhost --env FROZY_PROVIDER_PORT=4444 frozy/connector

Or using configuration file:

        docker run -it --rm --net=host --mount type=bind,source=/dir/with/config,target=/tmp frozy/connector --config /tmp/connector.yaml

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

    Default: 22

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

  * ```FROZY_JOIN_TOKEN``` (```join.token```) - Join token value to use (mostly for automated
    connector deployment scenarios). If this is specified, Connector wouldn't
    prompt user for Join Token, but will instead use this value.

    Default: none (user is prompted for Join Token at the console)

Additional environment (configuration) variables for using access token API:
  * ```FROZY_RESOURCE_NAME``` (```auto_registration.resource```) - Name of the resource to register (mostly for
    automated connector deployment scenarios). Used without ```$FROZY_JOIN_TOKEN``
    and with ```$FROZY_ACCESS_TOKEN``` for automatically create and register join token.

  * ```FROZY_ACCESS_TOKEN``` (```auto_registration.access_token```) - Access token to use for backend authentication.

  * ```FROZY_CONSUMER_PORT``` (```auto_registration.conumer.port```) - If set then Consumer Join Token would be created
         (value of this variable would be used as a consumer port)

  * ```FROZY_PROVIDER_HOST``` (```auto_registration.provider.host```) - If set then Provider Join Token would be created
         (value of this variable would be used as a provider host)

  * ```FROZY_PROVIDER_PORT```  (```auto_registration.provider.port```) - If set then Provider Join Token would be created
         (value of this variable would be used as a provider port)

  * ```FROZY_BACKEND_URL```  (```frozy.api.http_root```) - URL for accessing backend. If this is set then
         FROZY_TIER is ignored.

    Default (if ```FROZY_TIER``` is set): ```$FROZY_TIER.frozy.cloud```

    Default (if ```FROZY_TIER``` is not set): ```frozy.cloud```

  * ```FROZY_FAIL_IF_EXISTS```  (```frozy.api.fail_if_exists```) - If set to anything then registration process
         will fail when resource with FROZY_RESOURCE_NAME already exists

# Build

        docker build -t frozy/connector .

# Use without Docker

To use without Docker against a sandbox tier:

        FROZY_TIER=sandbox FROZY_INSECURE=yes ./connector

Optionally prepend FROZY_CONFIG_DIR to specify directory to store configs
