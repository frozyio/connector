#!/bin/bash

FROZY_HTTP_SCHEMA=${FROZY_HTTP_SCHEMA:-"https"}

if [ -z "$FROZY_TIER" ]; then
  FROZY_HTTP_ROOT=${FROZY_HTTP_ROOT:-"$FROZY_HTTP_SCHEMA://frozy.cloud"}
  FROZY_BROKER_HOST=${FROZY_BROKER_HOST:-"broker.frozy.cloud"}
else
  FROZY_HTTP_ROOT=${FROZY_HTTP_ROOT:-"$FROZY_HTTP_SCHEMA://$FROZY_TIER.frozy.cloud"}
  FROZY_BROKER_HOST=${FROZY_BROKER_HOST:-"broker.$FROZY_TIER.frozy.cloud"}
fi

FROZY_BROKER_PORT=${FROZY_BROKER_PORT:-"22"}
FROZY_CONFIG_DIR=${FROZY_CONFIG_DIR:-"$HOME/.frozy-connector"}
mkdir -p "$FROZY_CONFIG_DIR"

function obtainConfig() {
  # Now we don't want "exit on failure" behavior
  set +e

  http_code=0
  curl_additionals=""
  if [ ! -z "$FROZY_INSECURE" ]; then
    curl_additionals="--insecure "
  fi

  while [ "$http_code" -ne 200 ]; do
    read -p 'Please paste a Join Token: ' join_token
    http_code=$(curl -s -o "$FROZY_CONFIG_DIR/tmp_response.txt" -w '%{http_code}' \
      -X POST -H "Content-Type: multipart/form-data" \
      --form "token=$join_token" \
      --form "key=@$FROZY_CONFIG_DIR/id_rsa.pub" \
      $curl_additionals \
      $FROZY_HTTP_ROOT/reg/v1/register)
    if [ "$http_code" -eq 0 ]; then
      cat <<EOM
Couldn't connect to Registration API. Are you using insecure tier (sandbox)
without FROZY_INSECURE environment variable set to "yes"?
EOM
    elif [ "$http_code" -ne 200 ]; then
      echo "Registration Server replied with an error $http_code: "
      cat $FROZY_CONFIG_DIR/tmp_response.txt
    fi
  done

  mv "$FROZY_CONFIG_DIR/tmp_response.txt" "$FROZY_CONFIG_DIR/config"
}

have_identity="no"
if [ -e "$FROZY_CONFIG_DIR/id_rsa" ] && [ -e "$FROZY_CONFIG_DIR/id_rsa.pub" ]; then
  have_identity="yes"
fi

have_config="no"
if [ -e "$FROZY_CONFIG_DIR/config" ]; then
  have_config="yes"
fi

if [ "$have_identity" != "yes" ]; then
  ssh-keygen -t rsa -b 2048 -f "$FROZY_CONFIG_DIR/id_rsa" -P ""
fi

if [ "$have_config" != "yes" ]; then
  obtainConfig
fi

read_config() {
  while IFS="=" read -r key value; do
    case "$key" in
      "role")     role="$value" ;;
      "host")     host="$value" ;;
      "port")     port="$value" ;;
      "resource") resource="$value" ;;
      "farm")     farm="$value" ;;
    esac
  done < "$FROZY_CONFIG_DIR/config"
}

read_config
poll_interval=15

case "$role" in
  consumer)
    echo "Listening at localhost:$port to consume resource $resource($farm) (broker at $FROZY_BROKER_HOST:$FROZY_BROKER_PORT)"
    echo "Use double Ctrl-C to exit"
    while true
    do
    	ssh -Ng -i "$FROZY_CONFIG_DIR/id_rsa" -o ServerAliveInterval=60 \
        -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
        -L $port:$farm.$resource:1025 -p $FROZY_BROKER_PORT $FROZY_BROKER_HOST
    	sleep $poll_interval
    done
    ;;
  provider)
    echo "Providing resource $resource from $host:$port (broker at $FROZY_BROKER_HOST:$FROZY_BROKER_PORT)"
    echo "Use double Ctrl-C to exit"
    while true
    do
    	ssh -Ng -i "$FROZY_CONFIG_DIR/id_rsa" -o ServerAliveInterval=60 \
        -o StrictHostKeyChecking=no -o IdentitiesOnly=yes \
        -R $farm.$resource:1025:$host:$port -p $FROZY_BROKER_PORT \
        $FROZY_BROKER_HOST
    	sleep $poll_interval
    done
    ;;
  default)
    echo "Misconfiguration detected. Unexpected role \"$role\""
esac
