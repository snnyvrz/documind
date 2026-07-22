# documind

AI-powered document processing platform built with React, Echo (Go Framework), FastAPI, gRPC, and PostgreSQL. Features JWT authentication, file uploads, asynchronous processing, and a microservice architecture designed for production.

## API authentication

The Go API requires `JWT_SECRET` before it can start:

```sh
cd api
JWT_SECRET='use-a-long-random-secret' go run .
```

Authentication endpoints are available at `http://localhost:1323`:

| Method | Path             | Body                                                              | Response                                     |
| ------ | ---------------- | ----------------------------------------------------------------- | -------------------------------------------- |
| `POST` | `/auth/register` | `{"email":"user@example.com","password":"at-least-8-characters"}` | `201` user id and email                      |
| `POST` | `/auth/sign-in`  | `{"email":"user@example.com","password":"at-least-8-characters"}` | `200` access token and refresh token         |
| `POST` | `/auth/refresh`  | `{"refreshToken":"..."}`                                          | `200` rotated access token and refresh token |
| `POST` | `/auth/sign-out` | `{"refreshToken":"..."}`                                          | `204` and revokes that refresh token         |

Access tokens last 15 minutes; refresh tokens last 7 days and are rotated each time they are exchanged. Users and refresh-token revocations are currently held in memory, so they are reset when the API restarts.
