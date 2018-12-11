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

# Environment variables

Connector behavior can be tuned by adjusting environment variables. For
docker-based scenarios you do this by using ```--env``` argument for ```docker run```.
Like this:

        docker run -it --rm --net=host --env FROZY_TIER=sandbox --env FROZY_INSECURE=yes frozy/connector

Available environment variables are:
  * ```FROZY_TIER``` - tier (dev/staging) of Frozy that is used.

    Default: none (use production tier)

  * ```FROZY_INSECURE``` - If set to 'yes' then ignore TLS certificate validation
    errors when accessing Registration API.

    Default: none (validate TLS certificate)

  * ```FROZY_BROKER_HOST``` - hostname of the Frozy Broker to connect to.

    Default (if ```FROZY_TIER``` is set): ```broker.$FROZY_TIER.frozy.cloud```

    Default (if ```FROZY_TIER``` is not set): ```broker.frozy.cloud```

  * ```FROZY_BROKER_PORT``` - Broker TCP port number

    Default: 22

  * ```FROZY_REGISTRATION_HTTP_SCHEMA``` - URL schema (http/https) that would be
    used be used to access Registration API Server.

    Default: ```https```

  * ```FROZY_REGISTRATION_HTTP_ROOT``` - Root URL of registration API server.

    Default (if ```FROZY_TIER``` is set): ```$FROZY_TIER.frozy.cloud```

    Default (if ```FROZY_TIER``` is not set): ```frozy.cloud```

  * ```FROZY_REGISTRATION_HTTP_URL``` - Path to ```register``` method of Registration
    API under ```$FROZY_REGISTRATION_HTTP_ROOT```

    Default: ```/reg/v1/register```

  * ```FROZY_CONFIG_DIR``` - Path (local to where Connector runs) to store
    connector configuration and key information

    Default: ```$HOME/.frozy-connector```

  * ```FROZY_JOIN_TOKEN``` - Join token value to use (mostly for automated
    connector deployment scenarios). If this is specified, Connector wouldn't
    prompt user for Join Token, but will instead use this value.

    Default: none (user is prompted for Join Token at the console)

# Build

        docker build -t frozy/connector .

# Use without Docker

To use without Docker against a sandbox tier:

        FROZY_TIER=sandbox FROZY_INSECURE=yes ./connector

Optionally prepend FROZY_CONFIG_DIR to specify directory to store configs
