run: stop up

recreate-app:
	docker-compose up -f deployments/docker-compose.yml -d --force-recreate --no-deps --build imgresizer_app

up:
	docker-compose -f deployments/docker-compose.yml up -d --build

stop:
	docker-compose -f deployments/docker-compose.yml stop

down:
	docker-compose -f deployments/docker-compose.yml down

test:
	docker-compose -f deployments/docker-compose.test.yml up --build --abort-on-container-exit
	docker-compose -f deployments/docker-compose.test.yml down --volumes