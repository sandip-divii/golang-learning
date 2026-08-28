// Package model holds the data shapes shared by every layer of the API.
//
// Nothing in here knows about HTTP, SQL or Gin. It is deliberately the most
// boring package in the project: one struct, no behaviour.
package model

import "time"

// User is one row of the `users` table and one object in a JSON response.
//
// The `json:"..."` tags control the key names the API sends and accepts.
// Without them Go would use the Go field names ("ID", "Name") instead of the
// lowercase names our API contract promises.
type User struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}
