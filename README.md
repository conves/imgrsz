### Image resizer microservice 
 
This is an image delivery Microservice. The service has a set of image files, stored in a local folder. 
It receives HTTP calls for those images on an endpoint (like `/image/img_012343.jpg`) and supports 
requests for various resolutions of those images. For instance, `/image/img_012343.jpg?size=300x400` 
will resize the original image at 300x400 and return it to the client. Its intended purpose is to serve images optimised 
for the device they’ll be displayed onto. For example, mobile devices will ask for lower resolutions while 
web applications will ask for higher resolutions or the original. 

Additional info: 
*  We do not know in advance the resolutions that the clients will ask for. 
*  It avoids resizing images that were previously resized to the same resolution (they're cached).
*  Other source images can be added in the folder at any time while the service is running. 
*  There's an endpoint to expose stats about the service, like: 
   *  The number of original files. 
   *  The number of resized files.
   *  Cache hits/misses. 
* It has a Dockerfile for running the service inside a container

#### Implementation details
It's built with Go 1.13 and exposes a http server on the port `8080`, with the following endpoints:
* `/image/{filename}?size=100x100` to serve images. The query string is optional.
* `/metrics` to export metrics from Prometheus agent (response time by statuses, number of cache hits/misses, etc.)
* `/docs` to serve a Swagger API documentation

Internally, it runs: 
* the web server, which, on its `/image` handler:
    * extracts image resizing details from the http request (original filename and size)
    * if there's no size provided, it tries to serve an original image, or respond with a 404 status
    * when there's a valid size in the query string, it tries to find a previously resized image by this particular size:
        * if image is to be found in cache (disk), it just serves it;
        * else:
            * it puts the image meta data (original filename, target size, etc) on a queue. This queue is abstracted through an interface, and the project comes with an implementation provided on top of a Redis list.  
            * it starts waiting for either:
                 * an ACK message on a bus (also an abstraction built on top of a Redis pub/sub). This ends up as a successful resize operation and the image can be served.
                 * a timeout (it can be configured through a flag at startup). In this case, it returns a 503 status to send a "too much load on the server" signal to the client.
* a configurable number of concurrent background workers. These workers:
    * extract new resize requests from the queue
    * do the actual image resizing and save the file on disk
    * once finished, a worker pushes an ACK message on a bus  

#### How to run it    
To start the containerized services (the app & Redis), simply run: 
```make run```

To run the all the tests (in a container), execute:
 ```make test```

**TODOs**
* add a distributed storage implementation for `ImageStore` interface
* add some cleanup (tearup & teardown) methods in tests
* improve Swagger docs
* add an architectural sketch to make it easier for potential reviewers to quickly grasp the internals
* extract the image resizing worker to a microservice of its own & adjust the docker-compose file
 