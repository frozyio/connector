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
and resumes what it done previously. Otherwise it checks ```join``` and
```auto_registration``` sections of YAML configuration.

If `join` section exists then it performs key registration action, receives
configuration and connects to broker with obtained configuration values.

In other case if `auto_registration` section exists connector registers a
resource, obtains join token, proceeds with key registration, and connects to
broker.

Finally if registration reply cache doesn't exist and neither `join` nor
`auto_registration` sections are specified in YAML configuration then connector
interactively asks for join token from stdin.

Minimal configuration file for use access token to register new resource and make provider
could be like that:

        auto_registration:
          access_token: qwerty12345
          resource: ResourceName
          provider:
            host: localhost
            port: 1234

Configuration file path could be specified via command line argument:

        ./connector --config /path/to/connector.yaml

In such case configuration will be read from the specified file, processed and
written to the default location.

# Environment variables

Connector behavior can be tuned by adjusting environment variables. For
docker-based scenarios you do this by using ```--env``` argument for
```docker run```. Like this:

        docker run -it --rm --net=host --env FROZY_TIER=sandbox --env FROZY_INSECURE=yes frozy/connector

Or if you'd like to use access token to register and run new provider:

        docker run -it --rm --net=host --env FROZY_TIER=sandbox --env FROZY_ACCESS_TOKEN=<access-token> --env FROZY_RESOURCE_NAME=Cat --env FROZY_PROVIDER_HOST=localhost --env FROZY_PROVIDER_PORT=4444 frozy/connector

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

  * ```FROZY_RESOURCE_NAME``` (```auto_registration.resource```) - Name of the
    resource to register (mostly for automated connector deployment scenarios).
    Used without ```$FROZY_JOIN_TOKEN``` and with ```$FROZY_ACCESS_TOKEN``` to
    automatically create and register join token.

  * ```FROZY_ACCESS_TOKEN``` (```auto_registration.access_token```) - Access
    token to use for backend authentication.

  * ```FROZY_CONSUMER_PORT``` (```auto_registration.consumer.port```) - If set
    then Consumer Join Token would be created (value of this variable would be
    used as a consumer port)

  * ```FROZY_PROVIDER_HOST``` (```auto_registration.provider.host```) - If set
    then Provider Join Token would be created (value of this variable would be
    used as a provider host)

  * ```FROZY_PROVIDER_PORT```  (```auto_registration.provider.port```) - If set
    then Provider Join Token would be created (value of this variable would be
    used as a provider port).

  * ```FROZY_BACKEND_URL```  (```frozy.api.http_root```) - URL for accessing
    backend. If this is set then ```FROZY_TIER``` is ignored.

    Default (if ```FROZY_TIER``` is set): ```$FROZY_TIER.frozy.cloud```

    Default (if ```FROZY_TIER``` is not set): ```frozy.cloud```

  * ```FROZY_FAIL_IF_EXISTS```  (```auto_registration.fail_if_exists```) - If
    set to anything then registration process will fail when resource with
    ```FROZY_RESOURCE_NAME``` already exists.

# Build

        make image

# Use without Docker

To use without Docker against a sandbox tier:

        FROZY_TIER=sandbox FROZY_INSECURE=yes ./connector

Optionally prepend FROZY_CONFIG_DIR to specify directory to store configs
