#!/bin/bash
docker run -d \
	-e POSTGRES_HOST_AUTH_METHOD=trust \
	-v /Users/gene/postgres/data:/var/lib/postgresql/data \
	-p 5432:5432 \
	postgres

