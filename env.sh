#!/bin/sh

set -e

if [ ! -f "env.sh" ]; then
    echo "$0 must be run from the root of the repository."
    exit 2
fi

# Create fake Go workspace if it doesn't exist yet.
workspace="$PWD/build/_workspace"
root="$PWD"
infidir="$workspace/src/github.com/infinetio"
if [ ! -L "$infidir/go-infinet" ]; then
    mkdir -p "$infidir"
    cd "$infidir"
    ln -s ../../../../../. go-infinet
    cd "$root"
fi

# Set up the environment to use the workspace.
GOPATH="$workspace"
export GOPATH

# Run the command inside the workspace.
cd "$infidir/go-infinet"
PWD="$infidir/go-infinet"

# Launch the arguments with the configured environment.
exec "$@"
