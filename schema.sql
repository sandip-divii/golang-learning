-- Run this once in phpMyAdmin (XAMPP MariaDB, port 3307)
-- before starting the server for the first time.
--
-- If you already created go_user_db for the net/http version of this project,
-- you can reuse it as-is and skip this file entirely -- the schema is identical.

CREATE DATABASE IF NOT EXISTS go_user_db
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

USE go_user_db;

CREATE TABLE IF NOT EXISTS users (
  id         INT AUTO_INCREMENT PRIMARY KEY,
  name       VARCHAR(100) NOT NULL,
  email      VARCHAR(255) NOT NULL UNIQUE,
  created_at TIMESTAMP    DEFAULT CURRENT_TIMESTAMP
);

-- The UNIQUE on email is the database's own safety net: even if the Go code
-- has a bug, MySQL refuses the duplicate and returns error 1062, which the
-- repository turns into a 409 Conflict.
