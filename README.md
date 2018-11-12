# Use with Docker

First of all you need to build image (that's until we have an organization at
Docker Hub). See below for instructions.

Once you have an image built you could do

        docker run -it --rm -p <host-port>:<container-port> frozy/connector

<container-port> should be what you have entered to frontend when obtaining join
token. <host-port> is whatever port you'd like to access service at.

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

        docker run --it -rm --net=host --env FROZY_TIER=sandbox frozy/connector

# Build

docker build -t frozy/connector .

# Use without Docker

To use without Docker against a sandbox tier:

        FROZY_TIER=sandbox FROZY_INSECURE=yes ./connector.sh

Optionally prepend FROZY_CONFIG_DIR to specify directory to store configs

# Use for Frozy development

To use against local deployment of Frozy first source connector environment
script:

        source _devops/_local/scripts/connector_env.sh
