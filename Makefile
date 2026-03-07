.PHONY: build run

# Build the image and tag it as 'flag-api'
build:
	docker build -t flag-api .

# Run the container, mapping port 8080
run:
	docker run -p 8080:8080 flag-api
