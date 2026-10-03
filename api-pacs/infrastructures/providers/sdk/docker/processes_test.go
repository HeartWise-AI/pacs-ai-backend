package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/docker/docker/client"
)

func TestInferenceProcessIDsIdentifiesSupervisedWorker(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ps_args") != "-eo pid,args" {
			t.Error("unexpected ps arguments")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"Titles":["PID","COMMAND"],"Processes":[["10","/usr/bin/python /usr/bin/supervisord"],["20","nginx: master process"],["30","/usr/bin/python /usr/local/bin/uvicorn main:app --port 8000"],["40","python pipeline_script.py"]]}`))
	}))
	defer s.Close()
	c, err := client.NewClientWithOpts(client.WithHost(s.URL), client.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pids, err := (&DockerSDK{Client: c}).InferenceProcessIDs(context.Background(), "test")
	if err != nil || len(pids) != 1 || pids[0] != "30" {
		t.Fatalf("pids=%v err=%v", pids, err)
	}
}
