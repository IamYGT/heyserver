package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/IamYGT/heyserver/internal/services/dockerctl"
)

type dockerOperations interface {
	Status(context.Context) (dockerctl.Status, error)
	Containers(context.Context) ([]dockerctl.Container, error)
	ContainerLogs(context.Context, string, int) (dockerctl.Logs, error)
	Images(context.Context) ([]dockerctl.Image, error)
	ContainerAction(context.Context, string, string) error
	PullImage(context.Context, string) error
	RemoveImage(context.Context, string) error
}

var dockerService dockerOperations = dockerctl.New()

func handleDockerStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := dockerService.Status(r.Context())
		if err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, status)
	}
}

func handleDockerContainers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		containers, err := dockerService.Containers(r.Context())
		if err != nil {
			jsonError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if containers == nil {
			containers = []dockerctl.Container{}
		}
		jsonResponse(w, http.StatusOK, containers)
	}
}

func handleDockerContainerLogs() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !dockerctl.ValidObjectID(id) {
			jsonError(w, http.StatusBadRequest, "invalid container id format")
			return
		}
		tail := 200
		if raw := strings.TrimSpace(r.URL.Query().Get("tail")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 1000 {
				jsonError(w, http.StatusBadRequest, "tail must be between 1 and 1000")
				return
			}
			tail = parsed
		}
		logs, err := dockerService.ContainerLogs(r.Context(), id, tail)
		if err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, logs)
	}
}

func handleDockerImages() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		images, err := dockerService.Images(r.Context())
		if err != nil {
			jsonError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if images == nil {
			images = []dockerctl.Image{}
		}
		jsonResponse(w, http.StatusOK, images)
	}
}

func handleDockerContainerControl() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		action := r.PathValue("action")
		if !dockerctl.ValidObjectID(id) {
			jsonError(w, http.StatusBadRequest, "invalid container id format")
			return
		}
		if !dockerctl.ValidContainerAction(action) {
			jsonError(w, http.StatusBadRequest, "invalid container action")
			return
		}
		if err := dockerService.ContainerAction(r.Context(), id, action); err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok", "action": action, "container": id})
	}
}

func handleDockerImagePull() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Name string `json:"name"`
		}
		if err := decodeStrictJSON(r, &request); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		request.Name = strings.TrimSpace(request.Name)
		if !dockerctl.ValidObjectID(request.Name) {
			jsonError(w, http.StatusBadRequest, "invalid image reference")
			return
		}
		if err := dockerService.PullImage(r.Context(), request.Name); err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok", "image": request.Name})
	}
}

func handleDockerImageDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !dockerctl.ValidObjectID(id) {
			jsonError(w, http.StatusBadRequest, "invalid image reference")
			return
		}
		if err := dockerService.RemoveImage(r.Context(), id); err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok", "image": id})
	}
}
