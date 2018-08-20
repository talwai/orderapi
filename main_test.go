package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

var OriginLatLng = LatLng{"12.9734", "77.5910"}
var DestLatLng = LatLng{"12.9527", "77.5848"}

// expected distance from Google Maps API between above two points
var expectedDistance = 3527

// tolerate a delta of this value when asserting route distance returned by Google Maps API
const DistanceToleranceThreshold = 500

func TestLatLngToString(t *testing.T) {
	assert := assert.New(t)

	assert.Equal("12.9734,77.5910", OriginLatLng.String())
	assert.Equal("12.9527,77.5848", DestLatLng.String())
}

func TestComputeDistance(t *testing.T) {
	assert := assert.New(t)

	// empire state building NYC
	origin := OriginLatLng.String()
	// freedom tower NYC
	dest := DestLatLng.String()

	d, err := computeDistance(origin, dest)
	assert.Nil(err)

	assert.NotNil(d)
	t.Logf("Distance %v", d)

	// should be roughly expectedDistance meters with an allowance of DistanceToleranceThreshold
	assert.InDelta(expectedDistance, d.Meters, DistanceToleranceThreshold)
}

func TestOrderResolve(t *testing.T) {
	assert := assert.New(t)

	// first resolve an order
	o := Order{OriginLatLng, DestLatLng}
	resolved, err := o.Resolve()

	assert.Nil(err)
	assert.NotNil(resolved, err)

	// retreive the same order from the database
	row := defaultOrderDatabase.db.QueryRow(
		`SELECT origin, destination, distance, status from orders where id=$1`,
		resolved.Id)

	var origin, destination, status string
	var distance int
	row.Scan(&origin, &destination, &distance, &status)

	// ensure that order details match
	assert.Equal(OriginLatLng.String(), origin)
	assert.Equal(DestLatLng.String(), destination)
	assert.InDelta(expectedDistance, distance, DistanceToleranceThreshold)
	assert.Equal(OrderStatusUnassign, status)
}

func TestCreateOrderValidInput(t *testing.T) {
	assert := assert.New(t)

	r := Router()
	srv := httptest.NewServer(r)
	client := srv.Client()
	orderEndpoint := fmt.Sprintf("%s/%s", srv.URL, "order")

	body := []byte(`{
		"origin": ["12.9734", "77.5910" ],
		"destination": ["12.9527", "77.5848" ]
	}`)

	reader := bytes.NewReader(body)

	resp, err := client.Post(orderEndpoint, "application/json", reader)
	assert.Nil(err)
	assert.NotNil(resp)

	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Equal("application/json", resp.Header.Get("Content-Type"))

	respBody, _ := ioutil.ReadAll(resp.Body)
	var resolved ResolvedOrder
	json.Unmarshal(respBody, &resolved)

	assert.InDelta(expectedDistance, resolved.Distance, DistanceToleranceThreshold)
	assert.Equal(OrderStatusUnassign, resolved.Status)
}

func TestCreateOrderInvalidInput(t *testing.T) {
	assert := assert.New(t)

	r := Router()
	srv := httptest.NewServer(r)
	client := srv.Client()

	type testCase struct {
		body []byte
		msg  string
	}

	testCases := []testCase{
		testCase{
			[]byte(`{
			"origin": ["bad_input"],
			"destination": ["40.7127", "-74.0134"]
		}`),
			"origin and destination must be valid lat, lng pairs",
		},
		testCase{
			[]byte(`{
			"origin": ["bad_input"],
			"destination": ["bad_input"]
		}`),
			"origin and destination must be valid lat, lng pairs",
		},

		testCase{
			[]byte(`{
			"origin": 1,
			"destination": "bogus"
		}`),
			"json: cannot unmarshal",
		},

		testCase{
			[]byte(`{
			"origin": -42,
			"destination": 100
		}`),
			"json: cannot unmarshal",
		},
	}

	orderEndpoint := fmt.Sprintf("%s/%s", srv.URL, "order")
	for _, tc := range testCases {
		reader := bytes.NewReader(tc.body)

		resp, err := client.Post(orderEndpoint, "application/json", reader)
		assert.Nil(err)
		assert.NotNil(resp)

		assert.Equal(http.StatusBadRequest, resp.StatusCode)
		assert.Equal("application/json", resp.Header.Get("Content-Type"))
		respBody, _ := ioutil.ReadAll(resp.Body)

		var errored errorResponse
		json.Unmarshal(respBody, &errored)
		assert.Contains(errored.Error, tc.msg)
	}
}

func TestUpdateOrderValidInput(t *testing.T) {
	assert := assert.New(t)

	// first insert an order
	id, _ := defaultOrderDatabase.InsertOrder(
		OriginLatLng.String(), DestLatLng.String(), OrderStatusUnassign, 1000,
	)

	r := Router()
	srv := httptest.NewServer(r)
	client := srv.Client()
	orderEndpoint := fmt.Sprintf("%s/%s/%d", srv.URL, "order", id)

	putData := []byte(`{"status": "taken"}`)
	req, _ := http.NewRequest(http.MethodPut, orderEndpoint, bytes.NewReader(putData))

	resp, _ := client.Do(req)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Equal("application/json", resp.Header.Get("Content-Type"))
	respBody, _ := ioutil.ReadAll(resp.Body)
	assert.Equal("{\"status\":\"SUCCESS\"}\n", string(respBody))

	// redoing the PUT request should cause a 409 Conflict response
	req, _ = http.NewRequest(http.MethodPut, orderEndpoint, bytes.NewReader(putData))
	resp, _ = client.Do(req)
	assert.Equal(http.StatusConflict, resp.StatusCode)
	assert.Equal("application/json", resp.Header.Get("Content-Type"))
	respBody, _ = ioutil.ReadAll(resp.Body)
	assert.Equal("{\"error\":\"ORDER_ALREADY_BEEN_TAKEN\"}\n", string(respBody))
}

func TestUpdateOrderInvalidInput(t *testing.T) {
	assert := assert.New(t)

	r := Router()
	srv := httptest.NewServer(r)
	client := srv.Client()

	// test with bad order IDs in URL
	badOrderIds := []string{"BOGUS ENTRY", "43.5466"}
	for _, bad := range badOrderIds {
		orderEndpoint := fmt.Sprintf("%s/%s/%s", srv.URL, "order", bad)
		putData := []byte(`{"status": "taken"}`)
		req, _ := http.NewRequest(http.MethodPut, orderEndpoint, bytes.NewReader(putData))
		resp, _ := client.Do(req)
		assert.Equal(http.StatusBadRequest, resp.StatusCode)
		respBody, _ := ioutil.ReadAll(resp.Body)
		assert.Contains(string(respBody), "{\"error\":")
	}

	missingOrderIds := []string{"99999", "-999", "0"}
	for _, bad := range missingOrderIds {
		orderEndpoint := fmt.Sprintf("%s/%s/%s", srv.URL, "order", bad)
		putData := []byte(`{"status": "taken"}`)
		req, _ := http.NewRequest(http.MethodPut, orderEndpoint, bytes.NewReader(putData))
		resp, _ := client.Do(req)
		assert.Equal(http.StatusNotFound, resp.StatusCode)
		respBody, _ := ioutil.ReadAll(resp.Body)
		assert.Contains(string(respBody), "{\"error\":")
	}

	// test with bad PUT data, unknown status
	badPutDatas := [][]byte{
		[]byte(`{"status": "bogus"}`),
		[]byte(`Not-Valid-JSON}`),
	}

	for _, bad := range badPutDatas {
		orderEndpoint := fmt.Sprintf("%s/%s/%d", srv.URL, "order", 1)
		putData := bad
		req, _ := http.NewRequest(http.MethodPut, orderEndpoint, bytes.NewReader(putData))
		resp, _ := client.Do(req)
		assert.Equal(http.StatusBadRequest, resp.StatusCode)
		respBody, _ := ioutil.ReadAll(resp.Body)
		assert.Contains(string(respBody), "{\"error\":")
	}
}

func TestListOrdersValidInput(t *testing.T) {
	assert := assert.New(t)

	r := Router()
	srv := httptest.NewServer(r)
	client := srv.Client()

	page := 1
	limit := 10
	ordersEndpoint := fmt.Sprintf("%s/%s?page=%d&limit=%d", srv.URL, "orders", page, limit)

	resp, err := client.Get(ordersEndpoint)

	assert.Nil(err)
	assert.Equal(http.StatusOK, resp.StatusCode)
}

func TestListOrdersInvalidInput(t *testing.T) {
	assert := assert.New(t)

	r := Router()
	srv := httptest.NewServer(r)
	client := srv.Client()

	// endpoints with bad page or limit parameters
	badEndpoints := []string{
		fmt.Sprintf("%s/%s?page=%s&limit=%s", srv.URL, "orders", "-1", "asd"),
		fmt.Sprintf("%s/%s?page=%s&limit=%s", srv.URL, "orders", "asd", "-1"),
		fmt.Sprintf("%s/%s?page=%s", srv.URL, "orders", "-1"),
		fmt.Sprintf("%s/%s?limit=%s", srv.URL, "orders", "-1"),
		fmt.Sprintf("%s/%s?page=%s", srv.URL, "orders", "asd"),
		fmt.Sprintf("%s/%s?limit=%s", srv.URL, "orders", "asd"),
	}

	for _, ep := range badEndpoints {
		resp, err := client.Get(ep)

		assert.Nil(err)
		assert.Equal(http.StatusBadRequest, resp.StatusCode)

	}
}

func TestRouting(t *testing.T) {
	assert := assert.New(t)

	r := Router()
	srv := httptest.NewServer(r)

	client := srv.Client()

	t.Logf("server url %s", srv.URL)
	orderEndpoint := fmt.Sprintf("%s/%s", srv.URL, "order")
	ordersEndpoint := fmt.Sprintf("%s/%s", srv.URL, "orders")
	bogusEndpoint := fmt.Sprintf("%s/%s", srv.URL, "bogus")

	// GET on /order should not be allowed
	resp, err := client.Get(orderEndpoint)
	assert.Nil(err)
	assert.NotNil(resp)
	assert.Equal(http.StatusMethodNotAllowed, resp.StatusCode)

	// POST on /orders should not be allowed
	resp, err = client.Post(ordersEndpoint,
		"application/json",
		bytes.NewReader([]byte(`{}`)),
	)
	assert.Nil(err)
	assert.NotNil(resp)
	assert.Equal(http.StatusMethodNotAllowed, resp.StatusCode)

	// GET on /bogus endpoint should 404
	resp, err = client.Get(bogusEndpoint)
	assert.Nil(err)
	assert.NotNil(resp)
	assert.Equal(http.StatusNotFound, resp.StatusCode)
}

func TestMain(m *testing.M) {
	// override database for testing
	defaultOrderDatabase, _ = NewOrderDatabase("localhost")

	os.Exit(m.Run())
}
