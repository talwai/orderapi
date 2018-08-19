#!/bin/bash

if hash docker-compose 2>/dev/null; then
        docker build
else
	echo "Docker-compose not available. Please install docker and docker-compose to proceed"
fi
