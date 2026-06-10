# Fiber v3 — Patrones y Reglas para AurelioMod

> Versión: v3.3.0 | Go 1.26.4 | Junio 2026
> Este skill evita errores comunes de migración v2→v3 y documenta los patrones
> que usamos en AurelioMod Control API.

## 🔴 Errores comunes v2→v3 (NO COMETER)

### 1. `c.BodyParser()` → `c.Bind().Body()`
```go
// ❌ v2 (no existe en v3)
var req CreateWorkspaceRequest
c.BodyParser(&req)

// ✅ v3
var req CreateWorkspaceRequest
if err := c.Bind().Body(&req); err != nil {
    return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
}
```

### 2. `c.AllParams()` → `c.Bind().URI()`
```go
// ❌ v2
params := c.AllParams()

// ✅ v3
var params MyStruct
c.Bind().URI(&params)
```

### 3. `c.ParamsInt("id")` → `c.Params("id")` + parse
```go
// ❌ v2
id := c.ParamsInt("id")

// ✅ v3 — Params() solo devuelve string
idStr := c.Params("id")
id, err := strconv.Atoi(idStr)
```

### 4. `app.Mount()` → `app.Use()`
```go
// ❌ v2
app.Mount("/api", subApp)

// ✅ v3
app.Use("/api", subApp)
```

### 5. `app.Listen()` requiere goroutine para hooks
```go
// ✅ v3 — hooks de shutdown solo funcionan si Listen corre en goroutine
go app.Listen(":8080")
// ... registrar hooks
app.Shutdown()
```

### 6. `fiber.Ctx` es interface, no struct
```go
// ✅ v3 — Ctx es interface, no se puede embed como struct
// Si necesitás custom context, usá fiber.NewWithCustomCtx()
```

---

## 🟢 Patrones AurelioMod

### App inicial
```go
app := fiber.New(fiber.Config{
    AppName:      "AurelioMod Control API",
    ServerHeader: "AurelioMod",
    Immutable:    false, // priorizamos rendimiento
})
```

### Routing — API versionada en `/v1`
```go
v1 := app.Group("/v1")

// Workspaces
v1.Get("/workspaces", authMiddleware, listWorkspaces)
v1.Post("/workspaces", authMiddleware, createWorkspace)
v1.Get("/workspaces/:id", authMiddleware, getWorkspace)
v1.Get("/workspaces/:id/decisions", authMiddleware, listDecisions)

// Auth (sin middleware de auth)
v1.Post("/auth/login", loginHandler)
v1.Post("/auth/refresh", authMiddleware, refreshHandler)

// Health
app.Get("/healthz", healthHandler)
```

### PASETO Auth middleware
```go
func authMiddleware(c fiber.Ctx) error {
    token := c.Get("Authorization")
    if token == "" || !strings.HasPrefix(token, "Bearer ") {
        return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
            "error": "missing or invalid Authorization header",
        })
    }
    // Validar con internal/paseto
    claims, err := tm.VerifyToken(strings.TrimPrefix(token, "Bearer "))
    if err != nil {
        return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
            "error": "invalid token",
        })
    }
    c.Locals("workspace_id", claims.Subject())
    return c.Next()
}
```

### Query params (paginación)
```go
func listDecisions(c fiber.Ctx) error {
    status := c.Query("status", "")        // "" = todos
    from   := c.Query("from", "")           // ISO 8601
    to     := c.Query("to", "")             // ISO 8601
    limit  := c.Query("limit", "50")        
    offset := c.Query("offset", "0")
    // ...
}
```

### Response estándar
```go
// Éxito
c.Status(fiber.StatusOK).JSON(fiber.Map{
    "data": result,
})

// Error
c.Status(fiber.StatusNotFound).JSON(fiber.Map{
    "error": "workspace not found",
})

// Paginación
c.Status(fiber.StatusOK).JSON(fiber.Map{
    "data":   items,
    "total":  total,
    "limit":  limit,
    "offset": offset,
})
```

### Neon DB queries
```go
// Usar sql.DB directo, NO ORM. Patrón existente en engine/audit/neon.go
db, err := sql.Open("postgres", dbURL)
db.SetMaxOpenConns(10)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(5 * time.Minute)
```

---

## 📦 Middleware Fiber que NO usamos

| Middleware | Razón |
|---|---|
| `fiber/middleware/cors` | Lo maneja KrakenD (Fase 2+) |
| `fiber/middleware/logger` | Usamos slog (structured JSON) |
| `fiber/middleware/recover` | ✅ sí, siempre |
| `fiber/middleware/compress` | Lo maneja KrakenD |
| `fiber/middleware/limiter` | Lo maneja KrakenD |
| `fiber/middleware/cache` | Usamos DragonflyDB directo |
