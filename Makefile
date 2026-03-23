compile-main:
	go build -o main.exe main.go

docker-start-raft:
	docker compose up

# 启动服务器实例
start-server0:
	./main.exe -me 0

start-server1:
	./main.exe -me 1

start-server2:
	./main.exe -me 2

