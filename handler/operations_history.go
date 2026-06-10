package handler

import (
	"net/http"

	"mmt-delivery/service"

	"github.com/gin-gonic/gin"
)

// GetOperationsHistory returns paginated, filtered operation history.
func (h *Handler) GetOperationsHistory(c *gin.Context) {
	var filter service.OperationsHistoryFilter
	if err := c.ShouldBindQuery(&filter); err != nil {
		Fail(c, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := h.svc.QueryOperationsHistory(filter)
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, resp)
}

// GetOperationsHistoryFilters returns available filter values for dropdowns.
func (h *Handler) GetOperationsHistoryFilters(c *gin.Context) {
	resp, err := h.svc.GetOperationsHistoryFilters()
	if err != nil {
		Fail(c, http.StatusInternalServerError, err.Error())
		return
	}
	OK(c, resp)
}
