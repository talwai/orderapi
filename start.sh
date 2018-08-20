if hash docker-compose 2>/dev/null; then
        docker-compose up -d
else
	echo "Docker-compose not available. Install docker > 1.13 and docker-compose to proceed"
	exit 1
fi
