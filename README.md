# bbolt-crud

Minimal Go REST API with CRUD over [bbolt](https://github.com/etcd-io/bbolt), an embedded key-value store. Single file, only stdlib + bbolt — no router framework.

## Run

```bash
go mod tidy
go run .
# server listens on :8080, db file is ./app.db
```

## Endpoints

| Method | Path          | Purpose       |
|--------|---------------|---------------|
| GET    | `/healthz`    | health check  |
| GET    | `/users`      | list all      |
| POST   | `/users`      | create        |
| GET    | `/users/{id}` | read one      |
| PUT    | `/users/{id}` | update/upsert |
| DELETE | `/users/{id}` | delete        |

## Try it

```bash
# create
curl -X POST localhost:8080/users \
  -H 'content-type: application/json' \
  -d '{"id":"u1","name":"Kunal","email":"kunal@example.com"}'

# list
curl localhost:8080/users

# read
curl localhost:8080/users/u1

# update
curl -X PUT localhost:8080/users/u1 \
  -H 'content-type: application/json' \
  -d '{"name":"Kunal D","email":"kd@example.com"}'

# delete
curl -X DELETE localhost:8080/users/u1
```

## Design notes

- **One bucket, JSON values.** bbolt is just bytes-in, bytes-out, so values are marshaled JSON. Keeps the resource shape and validation in Go, not in the store.
- **`Store` wraps the DB.** Handlers never touch `*bolt.DB` directly. Swapping to Badger/Pebble/SQLite later means rewriting `Store`, not the HTTP layer.
- **`bbolt` transactions are mandatory** — every read goes through `View`, every write through `Update`. The lib won't let you read outside a tx.
- **`http.ServeMux` only.** Go 1.22+'s mux is enough for path prefixes; no need for chi/gin here. The `/users` vs `/users/` split handles collection vs item routing cleanly.
- **POST is create-only, PUT is upsert.** POST returns 409 if the id exists. PUT ignores any id in the body and uses the path id as the source of truth.
