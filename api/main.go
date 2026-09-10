package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"gorm.io/gorm"
)

func main() {
	_ = godotenv.Load()
	if migrateCommand() {
		return
	}
	if err := validateProductionAuthConfig(); err != nil {
		panic(err)
	}
	if embeddingDimensions() != maxEmbeddingDimensions {
		panic("EMBEDDING_DIMENSIONS must be 768 because the database uses vector(768)")
	}
	if err := runMigrations(os.Getenv("DATABASE_URL")); err != nil {
		panic("migrate document database: " + err.Error())
	}

	uploadDirectory, err := uploadDirectory()
	if err != nil {
		panic("determine upload directory: " + err.Error())
	}

	postgresStore, err := openDocumentStore(os.Getenv("DATABASE_URL"))
	if err != nil {
		panic("connect to document database: " + err.Error())
	}
	defer postgresStore.Close()
	store := documentStore(postgresStore)
	storage, err := configuredDocumentStorage(context.Background(), uploadDirectory)
	if err != nil {
		panic("configure document storage: " + err.Error())
	}
	if references, ok := store.(uploadReferenceStore); ok {
		reconcileContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := reconcileOrphanedUploads(reconcileContext, storage, references); err != nil {
			// Object cleanup is deliberately independent from database admission.
			// A temporary storage/listing failure must not prevent API startup.
			println("reconcile orphaned uploads: " + err.Error())
		}
		cancel()
	}
	workerConfig, err := loadWorkerConfig()
	if err != nil {
		panic("configure document worker: " + err.Error())
	}

	metrics := newMetrics()
	postgresStore.metrics = metrics
	e := newServerWithMetrics(uploadDirectory, store, storage, metrics)
	rootContext, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	workerDone := startWorker(rootContext, storage, store, workerConfig, metrics)
	startConfig := echo.StartConfig{Address: ":1323", GracefulTimeout: 30 * time.Second}
	if err := startConfig.Start(rootContext, e); err != nil && !errors.Is(err, http.ErrServerClosed) {
		e.Logger.Error("failed to start server", "error", err)
	}
	<-workerDone
}

func validateProductionAuthConfig() error {
	if authMode() == "development" {
		return nil
	}
	secret := os.Getenv("AUTH_JWT_SECRET")
	if secret == "" {
		return errors.New("AUTH_JWT_SECRET is required")
	}
	if secret == developmentJWTSecret {
		return errors.New("AUTH_JWT_SECRET must not use the development secret")
	}
	if len(secret) < 32 {
		return errors.New("AUTH_JWT_SECRET must be at least 32 characters")
	}
	if os.Getenv("AUTH_COOKIE_SECURE") != "true" || os.Getenv("AUTH_REQUIRE_HTTPS") != "true" {
		return errors.New("production requires AUTH_COOKIE_SECURE=true and AUTH_REQUIRE_HTTPS=true")
	}
	return nil
}

func authMode() string {
	return getenv("AUTH_MODE", "production")
}

func uploadDirectory() (string, error) {
	if directory := os.Getenv("UPLOAD_DIRECTORY"); directory != "" {
		return directory, nil
	}

	dataDirectory := os.Getenv("XDG_DATA_HOME")
	if dataDirectory == "" {
		homeDirectory, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataDirectory = filepath.Join(homeDirectory, ".local", "share")
	}

	return filepath.Join(dataDirectory, "documind", "uploads"), nil
}

func configuredDocumentStorage(ctx context.Context, directory string) (documentStorage, error) {
	if getenv("OBJECT_STORAGE_ENDPOINT", "") != "" {
		return newObjectStorage(ctx)
	}
	return newFilesystemStorage(directory), nil
}

func newServer(uploadDirectory string, store documentStore, storages ...documentStorage) *echo.Echo {
	return newServerWithMetrics(uploadDirectory, store, firstStorage(storages), newMetrics())
}

func firstStorage(storages []documentStorage) documentStorage {
	if len(storages) > 0 {
		return storages[0]
	}
	return nil
}

func newServerWithMetrics(uploadDirectory string, store documentStore, storage documentStorage, metrics *metrics) *echo.Echo {
	e := echo.New()

	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	e.GET("/", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"message": "Hello, World!"})
	})
	e.GET("/health", func(c *echo.Context) error { return c.JSON(http.StatusOK, map[string]string{"status": "ok"}) })
	e.GET("/ready", func(c *echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), time.Second)
		defer cancel()
		if err := store.Ping(ctx); err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "dependency": "database"})
		}
		connection, err := net.DialTimeout("tcp", processorAddress(), time.Second)
		if err != nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "dependency": "document_processor"})
		}
		_ = connection.Close()
		return c.JSON(http.StatusOK, map[string]string{"status": "ready"})
	})

	if storage == nil {
		storage = newFilesystemStorage(uploadDirectory)
	}
	handler := newDocumentHandlerWithMetrics(uploadDirectory, store, storage, metrics)
	e.GET("/metrics", handler.metrics.handler)
	var database *gorm.DB
	if postgresStore, ok := store.(*postgresDocumentStore); ok {
		database = postgresStore.database
	}
	auth, err := newAuthenticator(database)
	if err != nil {
		panic(err)
	}
	auth.routes(e)
	documents := e.Group("/documents")
	documents.Use(auth.middleware)
	documents.POST("", handler.Upload)
	documents.GET("", handler.List)
	documents.GET("/:id", handler.Get)
	documents.GET("/:id/chunks", handler.Chunks)
	documents.GET("/:id/file", handler.File)
	documents.HEAD("/:id/file", handler.File)
	documents.DELETE("/:id", handler.Delete)
	documents.POST("/:id/retry", handler.Retry)
	documents.GET("/:id/questions", handler.Questions)
	documents.POST("/:id/questions", handler.Ask)

	return e
}
