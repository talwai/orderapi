# Setup
## Prerequisites
- A recent version of `docker` and `docker-compose`
- (for testing, building locally): go > 1.10 and `dep`


## Running
```bash
$ ./start.sh
```

will set up the API and link it to a Postgres database via `docker-compose`. The API listens on 
port 8080. Please ensure that this port is free on your machine 

## Database schema
The database schema used by Postgres is available under `release/db/SCHEMAS.sql`. This schema
is embedded into the Docker image used by `docker-compose`

## Building

### Dev build
```bash
$ docker build .
```

### Release build

Dockerfiles for `api` and `db` are available under `release`

The Docker images were built with:
```bash
$ docker build --tag talwai/orderapi_api release/api
$ docker build --tag talwai/orderapi_db release/db
```

## Testing
```bash
# first clone the repo into a valid directory for dep e.g. $GOPATH/src/github.com/talwai/orderapi
$ dep ensure
$ go test .
```

# Notes
## POST /order
The preferred units and format for distance returned by the `POST /order` endpoint is meters as an integer e.g. 
{
   "id": 1,
   "distance": 1450, // meters
   "status": "UNASSIGN"
}

There is strict checking on the latitudes and longitudes submitted in `POST /order`. Latitudes must be numeric, range between -90 and 90. Longitudes must be numeric, range between -180 and 180

Any request not satisyfing the latitude, longitude criteria will be rejected with a 400.

Distance is computed as the Driving distance returned by the Google Maps Distance Matrix API


## PUT /order
Code assumes that there are only two distinct statuses allowed for orders: "UNASSIGN" and "TAKEN". 

PUT /order/:id allows changing status from UNASSIGN -> TAKEN. Changing status from TAKEN -> UNASSIGN
is also allowed

Changing any other fields on the order, such as `distance` is not allowed.

No two requests can take the same order. This is enforced using row-level locks in a Postgres transaction. See `database.go:OrderDatabase.UpdateOrderStatus` for details

## GET/orders?page=:page&limit=:limit
`limit` specifies the number of records to return in a single request. 
If `page` is sent along with `limit`, the table is split into `ceiling(N/limit)` pages where N is the total number of records in the table. 
A single page of records is sent as denoted by the `page` number.

If `page` is sent without `limit`, a default limit of 20 is assumed, and the behaviour is the same as described above.

If neither `page` nor `limit` is specified, a default limit of 20 is assumed, and the 1st page of records is sent.

If `limit` is sent without `page` the first `limit` records in the table are sent

The maximum value for `limit` is 100. 

No more than a 100 records will be serve in a single request. If users wish to retreive more than 100 records, they can use `page` along with `limit` to page through the table.

## General
Only the methods and endpoints specified in the backend.md document have been implemented in the API i.e. POST /order, GET /orders, PUT /order/:id

Any request to unspecified endpoints will give a 404 error. Using a method not allowed on an endpoint will give a 405 error e.g. POST /orders is not allowed.
