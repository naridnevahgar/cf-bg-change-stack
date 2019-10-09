#!/bin/bash

set -e

echo -e "\nGenerating Binary..."

CURRENTDIR=`pwd`

go build -o $CURRENTDIR/out/cf-bg-change-stack
