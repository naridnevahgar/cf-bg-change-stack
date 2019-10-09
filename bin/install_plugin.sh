#!/usr/bin/env bash
CURRENTDIR=`pwd`
PLUGIN_PATH="$CURRENTDIR/out/cf-bg-change-stack"

cf install-plugin "$PLUGIN_PATH" -f