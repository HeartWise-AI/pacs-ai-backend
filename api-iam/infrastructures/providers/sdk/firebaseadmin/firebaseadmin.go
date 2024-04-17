// TODO: abstract firebase method access

package firebaseadmin

import (
	"context"
	"log"

	firebase "firebase.google.com/go/v4"
	"google.golang.org/api/option"
)

type FirebaseAdminSDK struct {
	App *firebase.App
}

// NewApp create new firebase app
func NewApp(ctx context.Context, configPath, projectID string) (*FirebaseAdminSDK, error) {
	// get private key
	opt := option.WithCredentialsFile(configPath)

	// get project id
	config := &firebase.Config{ProjectID: projectID}

	app, err := firebase.NewApp(ctx, config, opt)
	if err != nil {
		log.Println("firebase admin sdk init failed:", err)
		return nil, err
	}

	return &FirebaseAdminSDK{
		App: app,
	}, nil
}
