# Go Load Balancer

A load balancer sits in front of your servers and decides which one handles each incoming request. Instead of all traffic hitting one server, it gets spread across many — so no single server gets overwhelmed. This project is a working load balancer you can run locally, built entirely with Go's standard library.

---

## What It Does

When a request comes in, the load balancer picks a backend server based on your chosen strategy, forwards the request, and returns the response to the client — completely transparently. In the background, it continuously checks if your servers are healthy and automatically stops sending traffic to any server that goes down, then brings it back once it recovers.

---

## Requirements

- Go 1.21 or higher — [download here](https://go.dev/dl/)
- A terminal (any OS)

Verify your Go installation:
```bash
go version
```

---

## Setup

Clone or download this project, then navigate into the folder:

```bash
cd loadbalancer
```

No extra packages to install — everything uses Go's standard library.

---

## Running It

You'll need **4 terminal windows** open at the same time.

**Terminals 1, 2, 3 — start the backend servers:**
```bash
go run backend_server.go 9001
go run backend_server.go 9002
go run backend_server.go 9003
```

**Terminal 4 — start the load balancer:**
```bash
go run main.go
```

The load balancer is now running at `http://localhost:8080`.

---

## Sending Requests

Open a fifth terminal and send a request:
```bash
curl http://localhost:8080/
```

Each request will be answered by a different backend server. Send several in a row to see the distribution in action:
```bash
for i in {1..6}; do curl http://localhost:8080/; echo; done
```

---

## Checking Stats

Visit this URL in your browser or via curl at any time:
```bash
curl http://localhost:8080/lb-stats
```

This shows how many requests each backend has handled, how many connections are active right now, whether each server is healthy, and total uptime.

---

## Testing Health Checks

Stop one of the backend servers by pressing `Ctrl+C` in its terminal. Within 10 seconds, the load balancer will notice and stop sending traffic to it. All requests will automatically go to the remaining healthy servers.

Start the server again and the load balancer will detect the recovery and resume sending it traffic — no restart needed.

---

## Configuration

All settings live in `config.json`. Open it to change anything:

| Setting | What it controls |
|---|---|
| `port` | The port the load balancer listens on |
| `algorithm` | How requests are distributed (see below) |
| `health_check_interval_seconds` | How often to ping backends |
| `backends` | List of backend server URLs and their weights |

### Algorithms

Change the `"algorithm"` value in `config.json` to switch strategies:

- **`round_robin`** — requests cycle evenly through all servers in order. Good default for servers with equal capacity.
- **`weighted_round_robin`** — servers with a higher `weight` value receive proportionally more traffic. Use this when some servers are more powerful than others.
- **`least_connections`** — each new request goes to whichever server currently has the fewest active connections. Best when requests vary a lot in how long they take.

After changing `config.json`, restart the load balancer for changes to take effect.

---

## Project Files

| File | Purpose |
|---|---|
| `main.go` | The load balancer |
| `backend_server.go` | Dummy backend server used for testing |
| `config.json` | All configuration |
| `go.mod` | Go module definition |
