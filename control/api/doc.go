// Package controlapi implements the AurelioMod Control API using Fiber v3.
// Routes are versioned under /v1 and protected by PASETO v4 auth tokens.
//
// Endpoints:
//
//	POST   /v1/auth/login          — workspace login (api_key → PASETO token)
//	POST   /v1/auth/refresh        — refresh PASETO token
//	GET    /v1/workspaces          — list workspaces
//	POST   /v1/workspaces          — create workspace
//	GET    /v1/workspaces/:id       — get workspace
//	GET    /v1/workspaces/:id/decisions — decision history with filters
package controlapi
