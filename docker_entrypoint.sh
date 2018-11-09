#!/bin/bash
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null && pwd )"

exit_func() {
  echo "Shutdown requested. Shutting down."
  exit 0
}

trap exit_func SIGTERM SIGINT

"$DIR/connector.sh" $@
