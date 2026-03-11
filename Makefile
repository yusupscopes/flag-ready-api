.PHONY: build run

# Build the image and tag it as 'flag-api'
build:
	docker build -t flag-api .

# Run the container, mapping port 3000
run:
	docker run -p 3000:3000 flag-api
