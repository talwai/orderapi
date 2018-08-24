package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"time"

	_ "github.com/lib/pq"
)

var (
	OrderAlreadyTakenError    = errors.New("ORDER_ALREADY_BEEN_TAKEN")
	OrderAlreadyUnassignError = errors.New("ORDER_ALREADY_UNASSIGN")
)

// OrderDatabase provides a wrapper around a database/sql connection
// to interact and mutate the `orders` table in Postgres
// Schema for the orders table can be found in release/db/SCHEMAS.sql
type OrderDatabase struct {
	db *sql.DB
}

// NewOrderDatabase creates a new OrderDatabase
func NewOrderDatabase(host string) (od *OrderDatabase, err error) {
	connStr := fmt.Sprintf(
		"host=%s user=postgres dbname=postgres password=password sslmode=disable",
		host,
	)
	db, err := sql.Open("postgres", connStr)

	if err != nil {
		return
	}

	od = &OrderDatabase{db}
	return
}

// SelectOrder selects an order from the database by id
func (od *OrderDatabase) SelectOrder(orderId int) (status string, err error) {
	err = od.db.QueryRow(`SELECT status FROM orders WHERE id = $1`, orderId).Scan(&status)
	if err != nil {
		return
	}

	log.Printf("retrieved order: %d, status: %s", orderId, status)
	return
}

// InsertOrder inserts an order into the database
func (od *OrderDatabase) InsertOrder(
	origin, destination, status string, distance int) (orderId int, err error) {

	err = od.db.QueryRow(`INSERT INTO orders(origin, destination, distance, status)
		VALUES($1, $2, $3, $4) RETURNING id`,
		origin, destination,
		distance, status,
	).Scan(&orderId)

	if err != nil {
		return
	}

	log.Printf("new order created: %d", orderId)
	return
}

// timeout in milliseconds for the UpdateOrderStatus transaction
const ctxTimeoutMs = 2000

// UpdateOrderStatus updates an order's status from UNASSIGN -> taken, or vice versa

// It initiates a transaction and acquires a row-level lock for the order
// being updated

// A configurable timeout of 2 seconds is enforced via context.Context to ensure that
// a transaction does not hold a lock for too long. If the context expires, the
// transaction is rolled back
func (od *OrderDatabase) UpdateOrderStatus(orderId int, newStatus string) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), ctxTimeoutMs*time.Millisecond)
	defer cancel()

	// Use a Postgres transaction to lock an Order row for update
	// If two requests try to update the same order, first one to lock the row wins
	tx, err := od.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
			return
		}

		err = tx.Commit()
	}()

	// FOR UPDATE clause acquires a row-level lock
	// https://www.postgresql.org/docs/9.6/static/explicit-locking.html#LOCKING-ROWS
	var status string
	err = tx.QueryRow(`SELECT status FROM orders where id=$1 FOR UPDATE`,
		orderId).Scan(&status)

	if err != nil {
		return err
	}

	// if we encounter a `taken` order, it must have been updated by an earlier transaction
	if status == newStatus {
		if newStatus == OrderStatusTaken {
			return OrderAlreadyTakenError
		} else if newStatus == OrderStatusUnassign {
			return OrderAlreadyUnassignError
		}
	}

	// ensure that we only allow UNASSSIGN -> taken transition
	row := tx.QueryRow(
		`UPDATE orders SET status = $1 WHERE id = $2 and status = $3 RETURNING id, status`,
		newStatus, orderId, status,
	)

	var updatedStatus string
	var updatedId int

	err = row.Scan(&updatedId, &updatedStatus)
	if err != nil {
		return err
	}

	return nil
}

// countOrders counts the orders in the database
func (od *OrderDatabase) countOrders() (int, error) {
	var count int

	err := od.db.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&count)
	return count, err
}

// RetrieveOrders retrieves a page of orders given a limit i.e. page size, and a page number
// It uses Postgres's LIMIT / OFFSET paging convention.
func (od *OrderDatabase) RetrieveOrders(limit, page int) ([]ResolvedOrder, error) {
	count, err := od.countOrders()
	if err != nil {
		return []ResolvedOrder{}, errors.New("failed to count orders")
	}

	if count == 0 {
		// if no orders are in the database, don't count it as a failure
		// rather just return an empty result set to the client
		return []ResolvedOrder{}, nil
	}

	totalPages := int(math.Ceil(float64(count) / float64(limit)))
	log.Printf("total pages: %d. count %d limit %d", totalPages, count, limit)
	resolved := make([]ResolvedOrder, 0, count)

	pageOffset := limit * (page - 1)

	rows, err := od.db.Query(`SELECT id, distance, status FROM orders LIMIT $1 OFFSET $2`,
		limit, pageOffset)
	defer rows.Close()

	if err != nil {
		return resolved, err
	}

	// add a ResolvedOrder for each row
	for rows.Next() {
		var id, distance int
		var status string

		rows.Scan(&id, &distance, &status)

		resolved = append(
			resolved,
			ResolvedOrder{Id: id, Distance: distance, Status: status},
		)
	}

	return resolved, rows.Err()
}
