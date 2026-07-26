package main

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

func main() {
	_ = godotenv.Load()

	uploadDirectory, err := uploadDirectory()
	if err != nil {
		panic("determine upload directory: " + err.Error())
	}

	e := newServer(uploadDirectory)

	if err := e.Start(":1323"); err != nil {
		e.Logger.Error("failed to start server", "error", err)
	}
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

func newServer(uploadDirectory string) *echo.Echo {
	e := echo.New()

	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	e.GET("/", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"message": "Hello, World!"})
	})

	e.POST("/documents", newDocumentHandler(uploadDirectory).Upload)

	return e
}
