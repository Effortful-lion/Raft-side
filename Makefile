compile-main:
	go build -o main.exe main.go

docker-start-raft:
	docker compose up

