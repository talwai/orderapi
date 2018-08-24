package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"
	"googlemaps.github.io/maps"
)

const (
	GoogleMapsAPIKey = "AIzaSyCZzW8pCgGnqGoUSiL2s5GhUM9gMgz0QnI"

	OrderStatusUnassign = "UNASSIGN"
	OrderStatusTaken    = "taken"

	DefaultOrderPage  = 1
	DefaultOrderLimit = 20
	MaxOrderLimit     = 20
)

// mapsClient is the default connector to the Google Maps API
var mapsClient *maps.Client

// defaultOrderDatabase is the default handle to the orders table in Postgres
var defaultOrderDatabase *OrderDatabase

func init() {
	client, err := maps.NewClient(maps.WithAPIKey(GoogleMapsAPIKey))
	if err != nil {
		log.Fatalf("failed to initialize Google Maps client: %s", err)
	}

	mapsClient = client
	pgHost := os.Getenv("ORDERS_POSTGRES_HOST")
	if pgHost == "" {
		pgHost = "localhost"
	}

	defaultOrderDatabase, err = NewOrderDatabase(pgHost)
	if err != nil {
		log.Fatalf("failed to initialize postgres connection: %s", err)
	}
}

// LatLng is a type to capture a latitude, longitude sequnce
type LatLng []string

// validateOrdersListParam validates the `page` and `limit` parameters for GET /orders endpoint
func validateOrdersListParam(s []string) (int, error) {
	errorTxt := "badly formatted parameter: page and limit should be positive integers"
	if len(s) == 1 {
		i, err := strconv.Atoi(s[0])
		if err != nil {
			return i, err
		}

		if i <= 0 {
			return 0, errors.New(errorTxt)
		}

		return i, err
	}

	return 0, errors.New(errorTxt)
}

// IsValid returns if a LatLng is a valid sequence of numeric latitude and numeric longitude
func (l LatLng) IsValid() bool {
	if len(l) == 2 {
		lat, latErr := strconv.ParseFloat(l[0], 64)
		lng, lngErr := strconv.ParseFloat(l[1], 64)

		// if numeric, check that lat and lng lie within sane bounds
		if latErr == nil && lngErr == nil {
			return lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
		}
	}

	return false
}

// String convert HTTP args to textual lat,lng format required by Google Maps
func (l LatLng) String() string {
	if !l.IsValid() {
		return "Invalid Latitude Longitude Input!"
	}

	return fmt.Sprintf("%s,%s", l[0], l[1])
}

// Order consists of the order parameters submitted by a client at the time of creation
type Order struct {
	Origin      LatLng `json:"origin"`
	Destination LatLng `json:"destination"`
}

// ResolvedOrder describes an order after it has been validated by Google Maps and persisted
type ResolvedOrder struct {
	Id       int    `json:"id,omitempty"`
	Distance int    `json:"distance,omitempty"`
	Status   string `json:"status,omitempty"`
}

// orderUpdate describes an update made to an order's status
type orderUpdate struct {
	Status string `json:"status"`
}

// errorResponse describes an error encountered when serving a client request
type errorResponse struct {
	Error string `json:"error"`
}

// computeDistance uses the Google Maps Distance Matrix API to compute
// a driving distance between `origin` and `destination`
// origin and destination must be lat,lng strings such as "40.7484,-73.9857"
func computeDistance(origin string, destination string) (maps.Distance, error) {
	r := &maps.DistanceMatrixRequest{
		Origins:       []string{origin},
		Destinations:  []string{destination},
		DepartureTime: `now`,
		Units:         `UnitsMetric`,
		Mode:          maps.TravelModeDriving,
	}

	// degenerate value for distance. should be overwritten by the API response
	minDistance := maps.Distance{
		HumanReadable: "",
		Meters:        math.MaxInt64,
	}

	distance, err := mapsClient.DistanceMatrix(context.Background(), r)
	if err != nil {
		log.Printf("failed to retrieve distance: %s", err)
		return minDistance, err
	}

	if len(distance.Rows) == 0 {
		log.Printf("failed to retrieve distance: %s", err)
		return minDistance, errors.New("No routes found by Google Maps")
	}

	for _, row := range distance.Rows {
		for _, elem := range row.Elements {
			if elem.Distance.Meters < minDistance.Meters {
				minDistance = elem.Distance
			}
		}
	}

	return minDistance, err
}

// Resolve converts a plain Order into a ResolvedOrder
// by computing route distance and inserting a corresponding record into the database
func (o *Order) Resolve() (resolved ResolvedOrder, err error) {
	distance, err := computeDistance(o.Origin.String(), o.Destination.String())
	if err != nil {
		log.Printf("failed to compute route distance: %s", err)
		return
	}
	log.Printf("computed route distance: %d", distance.Meters)

	orderId, err := defaultOrderDatabase.InsertOrder(
		o.Origin.String(), o.Destination.String(), OrderStatusUnassign,
		distance.Meters,
	)

	if err != nil {
		log.Printf("failed to insert order into database: %s", err)
		return
	}

	resolved = ResolvedOrder{
		orderId,
		distance.Meters,
		OrderStatusUnassign,
	}

	return
}

// createOrder accepts a JSON `Order` over HTTP and places a new order
func createOrder(w http.ResponseWriter, r *http.Request) {
	dec := json.NewDecoder(r.Body)
	enc := json.NewEncoder(w)

	order := Order{}
	err := dec.Decode(&order)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		resp := errorResponse{
			Error: fmt.Sprintf("%v", err),
		}

		enc.Encode(resp)
		return
	}

	if !(order.Origin.IsValid() && order.Destination.IsValid()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		resp := errorResponse{
			Error: "origin and destination must be valid lat, lng pairs",
		}

		enc.Encode(resp)
		return
	}

	log.Printf("order origin: %s", order.Origin)
	log.Printf("order destination: %s", order.Destination)

	resolved, err := order.Resolve()

	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		resp := errorResponse{
			Error: fmt.Sprintf("%v", err),
		}

		enc.Encode(resp)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	enc.Encode(resolved)
	return
}

// listOrders lists the orders on record. It accepts `page` and `limit` query parameters
// which are enforced as positive integers
func listOrders(w http.ResponseWriter, r *http.Request) {
	enc := json.NewEncoder(w)

	params := r.URL.Query()
	pageParam, pok := params["page"]
	limitParam, lok := params["limit"]

	page := DefaultOrderPage
	limit := DefaultOrderLimit

	var perr, lerr error

	switch {
	case pok && lok:
		// `limit` specifies the number of records to return in a single request
		// If `page` is sent along with `limit`, the table is split into `ceiling(N/limit)` pages
		// where N is the total number of records in the table. A single page of records is sent
		// as denoted by the `page` number.
		page, perr = validateOrdersListParam(pageParam)
		limit, lerr = validateOrdersListParam(limitParam)

		if lerr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			resp := errorResponse{
				Error: lerr.Error(),
			}

			enc.Encode(resp)
			return
		}

		if perr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			resp := errorResponse{
				Error: perr.Error(),
			}

			enc.Encode(resp)
			return
		}
	case pok && !lok:
		// If `page` is sent without `limit`, a default limit is assumed
		// and the behaviour is the same as described above
		page, perr = validateOrdersListParam(pageParam)
		if perr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			resp := errorResponse{
				Error: perr.Error(),
			}

			enc.Encode(resp)
			return
		}
	case lok && !pok:
		// If `limit` is sent without `page` the first `limit` records in the table are sent
		limit, lerr = validateOrdersListParam(limitParam)
		if lerr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			resp := errorResponse{
				Error: lerr.Error(),
			}

			enc.Encode(resp)
			return
		}

		// The maximum value for `limit` is 100. No more than a 100 records will be served
		// in a single request. If users wish to retrieve more than 100 records, they can
		// use `page` along with `limit` to page through the table.
		if limit > MaxOrderLimit {
			limit = MaxOrderLimit
		}
	default:
		// If neither `page` nor `limit` is specified, stick to the default values
	}

	log.Printf("retrieving orders with limit %d page %d", limit, page)
	orders, err := defaultOrderDatabase.RetrieveOrders(limit, page)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		resp := errorResponse{
			Error: err.Error(),
		}

		enc.Encode(resp)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc.Encode(orders)
}

// updateOrder updates the status of an existing order
// allowing clients to `take` an order.
func updateOrder(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	enc := json.NewEncoder(w)
	orderIdVar, ok := vars["id"]

	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		resp := errorResponse{
			Error: "unable to parse order id from url",
		}

		enc.Encode(resp)
		return
	}

	// enforce that orderId is a valid integer
	orderId, err := strconv.Atoi(orderIdVar)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		resp := errorResponse{
			Error: "order ID must be a valid integer",
		}

		enc.Encode(resp)
		return
	}

	var req orderUpdate
	dec := json.NewDecoder(r.Body)
	err = dec.Decode(&req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		resp := errorResponse{
			Error: fmt.Sprintf("%v", err),
		}

		enc.Encode(resp)
		return
	}

	// Received an unknown status from the client
	// Will not allow a status transition
	if req.Status != OrderStatusTaken {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		resp := errorResponse{
			Error: fmt.Sprintf("Unknown status: %s", req.Status),
		}

		enc.Encode(resp)
		return
	}

	err = defaultOrderDatabase.TakeOrderIfUnassigned(orderId)

	if err != nil {
		switch err {
		case sql.ErrNoRows:
			// no rows in response indicates that
			// the database record for this id does not exist
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			resp := errorResponse{
				Error: fmt.Sprintf("No order present with id %d", orderId),
			}

			enc.Encode(resp)
		case OrderAlreadyTakenError:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			resp := errorResponse{
				Error: "ORDER_ALREADY_BEEN_TAKEN",
			}

			enc.Encode(resp)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			resp := errorResponse{
				Error: fmt.Sprintf("%v", err),
			}

			enc.Encode(resp)
		}

		return
	}

	// order was successfully taken
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc.Encode(orderUpdate{Status: "SUCCESS"})
}

func Router() *mux.Router {
	r := mux.NewRouter()

	r.Path("/order").Methods("POST").HandlerFunc(createOrder)
	r.Path("/orders").Methods("GET").HandlerFunc(listOrders)
	r.Path("/order/{id}").Methods("PUT").HandlerFunc(updateOrder)

	return r
}

func main() {
	r := Router()
	srv := &http.Server{
		Addr:    ":8080",
		Handler: r,
	}

	log.Printf("Starting the HTTP Server on 8080")
	log.Fatal(srv.ListenAndServe())
}
