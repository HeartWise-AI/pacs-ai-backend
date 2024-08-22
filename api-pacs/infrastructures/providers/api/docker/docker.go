package main

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

func GetImageFromDocker(ctx context.Context, imageBuffer []byte) error {
	fmt.Println("Running:")
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	// Assume we have an image buffer
	imageBuffer = []byte("This is a simulated image buffer")

	// Encode the image buffer to base64
	encodedBuffer := base64.StdEncoding.EncodeToString(imageBuffer)

	// Create and start the container
	resp, err := cli.ContainerCreate(ctx, &container.Config{
		Image: "alpine:latest",
		Cmd: []string{"sh", "-c", fmt.Sprintf(`
			echo "Original content:"
			echo "%s" | base64 -d
			echo "/nManipulated content:"
			echo "%s" | base64 -d | tr '[:lower:]' '[:upper:]'
		`, encodedBuffer, encodedBuffer)},
	}, nil, nil, nil, "")
	if err != nil {
		panic(err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		panic(err)
	}
	fmt.Printf("Container started with ID: %s\n", resp.ID)

	// // Wait for the container to finish
	// statusCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	// select {
	// case err := <-errCh:
	// 	if err != nil {
	// 		panic(err)
	// 	}
	// case <-statusCh:
	// }

	// // Get container logs
	// out, err := cli.ContainerLogs(ctx, resp.ID, container.LogsOptions{ShowStdout: true})
	// if err != nil {
	// 	panic(err)
	// }
	// io.Copy(os.Stdout, out)

	// // Remove the container
	// err = cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
	// if err != nil {
	// 	panic(err)
	// }
	return nil
}
