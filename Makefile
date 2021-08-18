bin/kinplacesecret:
	GOOS=linux go build -o bin/kinplacesecret
	GOOS=darwin go build -o bin/kinplacesecret-darwin