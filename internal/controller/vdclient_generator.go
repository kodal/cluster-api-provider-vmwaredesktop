package controller

import (
	"context"
	"os"

	"github.com/kodal/vmrest-go-client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func NewVDClient(ctx context.Context) (*vmrest.APIClient, context.Context) {
	logger := log.FromContext(ctx)

	url := os.Getenv("VMREST_URL")
	if url == "" {
		logger.Info("missing required env VMREST_URL")
		os.Exit(1)
	}
	username := os.Getenv("VMREST_USERNAME")
	if url == "" {
		logger.Info("missing required env VMREST_USERNAME")
		os.Exit(1)
	}
	password := os.Getenv("VMREST_PASSWORD")
	if url == "" {
		logger.Info("missing required env VMREST_PASSWORD")
		os.Exit(1)
	}

	config := vmrest.NewConfiguration()
	config.BasePath = url
	auth := vmrest.BasicAuth{
		UserName: username,
		Password: password,
	}
	ctx = context.WithValue(ctx, vmrest.ContextBasicAuth, auth)

	return vmrest.NewAPIClient(config), ctx
}
