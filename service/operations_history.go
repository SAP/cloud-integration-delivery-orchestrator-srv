package service

import (
	"fmt"
	"strings"
	"time"

	"mmt-delivery/db"

	"gorm.io/gorm"
)

// OperationsHistoryFilter holds all query parameters for the operations history endpoint.
type OperationsHistoryFilter struct {
	TenantIDs           []uint   `form:"tenantId"`
	ArtifactName        string   `form:"artifactName"`
	ArtifactTypes       []string `form:"artifactType"`
	PackageID           string   `form:"packageId"`
	RequestStates       []string `form:"requestState"`
	ImportStates        []string `form:"importState"`
	DeployStates        []string `form:"deployState"`
	DeliveryRuleID      *uint    `form:"deliveryRuleId"`
	DeliveryRequestName string   `form:"deliveryRequestName"`
	CreatedBy           string   `form:"createdBy"`
	DateFrom            string   `form:"dateFrom"`
	DateTo              string   `form:"dateTo"`
	HasError            *bool    `form:"hasError"`
	SortBy              string   `form:"sortBy"`
	SortDir             string   `form:"sortDir"`
	Page                int      `form:"page"`
	PageSize            int      `form:"pageSize"`
}

// OperationsHistoryItem is a single row in the history response.
type OperationsHistoryItem struct {
	ID                     uint      `json:"id"`
	ArtifactTechID         string    `json:"artifactTechID"`
	ArtifactName           string    `json:"artifactName"`
	ArtifactVersion        string    `json:"artifactVersion"`
	ArtifactType           string    `json:"artifactType"`
	PackageID              string    `json:"packageID"`
	TenantID               uint      `json:"tenantID"`
	TenantName             string    `json:"tenantName"`
	DeliveryRequestID      uint      `json:"deliveryRequestID"`
	DeliveryRequestName    string    `json:"deliveryRequestName"`
	DeliveryRuleName       string    `json:"deliveryRuleName"`
	TransportRequestNumber string    `json:"transportRequestNumber"`
	RequestState           string    `json:"requestState"`
	ImportState            string    `json:"importState"`
	DeployState            string    `json:"deployState"`
	SkipDeploy             bool      `json:"skipDeploy"`
	LastError              string    `json:"lastError"`
	CreatedBy              string    `json:"createdBy"`
	UpdatedBy              string    `json:"updatedBy"`
	CreatedAt              time.Time `json:"createdAt"`
	UpdatedAt              time.Time `json:"updatedAt"`
}

// OperationsHistoryResponse is the paginated response.
type OperationsHistoryResponse struct {
	Data     []OperationsHistoryItem `json:"data"`
	Total    int64                   `json:"total"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"pageSize"`
}

// OperationsHistoryFiltersResponse provides available filter values for the UI dropdowns.
type OperationsHistoryFiltersResponse struct {
	Tenants       []FilterOption `json:"tenants"`
	ArtifactTypes []string       `json:"artifactTypes"`
	DeliveryRules []FilterOption `json:"deliveryRules"`
	Operators     []string       `json:"operators"`
}

// FilterOption is a generic id+name pair for dropdowns.
type FilterOption struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// QueryOperationsHistory returns paginated, filtered operation history.
func (s *Service) QueryOperationsHistory(filter OperationsHistoryFilter) (OperationsHistoryResponse, error) {
	query := s.DB.Model(&db.ArtifactTenantOperation{}).
		Joins("JOIN cpi_tenants ON cpi_tenants.id = artifact_tenant_operations.tenant_id").
		Joins("JOIN delivery_requests ON delivery_requests.id = artifact_tenant_operations.delivery_request_id").
		Joins("LEFT JOIN delivery_rules ON delivery_rules.id = delivery_requests.delivery_rule_id")

	// Apply filters
	if len(filter.TenantIDs) > 0 {
		query = query.Where("artifact_tenant_operations.tenant_id IN ?", filter.TenantIDs)
	}
	if filter.ArtifactName != "" {
		query = query.Where("artifact_tenant_operations.artifact_name ILIKE ?", "%"+filter.ArtifactName+"%")
	}
	if len(filter.ArtifactTypes) > 0 {
		query = query.Where("artifact_tenant_operations.artifact_type IN ?", filter.ArtifactTypes)
	}
	if filter.PackageID != "" {
		query = query.Where("artifact_tenant_operations.package_id = ?", filter.PackageID)
	}
	if len(filter.RequestStates) > 0 {
		query = query.Where("artifact_tenant_operations.request_state IN ?", filter.RequestStates)
	}
	if len(filter.ImportStates) > 0 {
		query = query.Where("artifact_tenant_operations.import_state IN ?", filter.ImportStates)
	}
	if len(filter.DeployStates) > 0 {
		query = query.Where("artifact_tenant_operations.deploy_state IN ?", filter.DeployStates)
	}
	if filter.DeliveryRuleID != nil {
		query = query.Where("delivery_requests.delivery_rule_id = ?", *filter.DeliveryRuleID)
	}
	if filter.DeliveryRequestName != "" {
		query = query.Where("delivery_requests.name ILIKE ?", "%"+filter.DeliveryRequestName+"%")
	}
	if filter.CreatedBy != "" {
		query = query.Where("artifact_tenant_operations.created_by = ?", filter.CreatedBy)
	}
	if filter.DateFrom != "" {
		if t, err := time.Parse(time.RFC3339, filter.DateFrom); err == nil {
			query = query.Where("artifact_tenant_operations.updated_at >= ?", t)
		}
	}
	if filter.DateTo != "" {
		if t, err := time.Parse(time.RFC3339, filter.DateTo); err == nil {
			query = query.Where("artifact_tenant_operations.updated_at <= ?", t)
		}
	}
	if filter.HasError != nil && *filter.HasError {
		query = query.Where("artifact_tenant_operations.last_error != ''")
	}

	// Count total before pagination (use Session to avoid mutating query state)
	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return OperationsHistoryResponse{}, fmt.Errorf("failed to count operations: %w", err)
	}

	// Sort
	sortCol := allowedSortColumn(filter.SortBy)
	sortDir := "DESC"
	if strings.EqualFold(filter.SortDir, "asc") {
		sortDir = "ASC"
	}
	query = query.Order(sortCol + " " + sortDir)

	// Paginate
	page, pageSize := normalizePagination(filter.Page, filter.PageSize)

	// Select and scan
	var rows []struct {
		db.ArtifactTenantOperation
		TenantName          string
		DeliveryRequestName string
		DeliveryRuleName    string
	}
	err := query.
		Select(
			"artifact_tenant_operations.*",
			"cpi_tenants.name AS tenant_name",
			"delivery_requests.name AS delivery_request_name",
			"delivery_rules.name AS delivery_rule_name",
		).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&rows).Error
	if err != nil {
		return OperationsHistoryResponse{}, fmt.Errorf("failed to query operations history: %w", err)
	}

	// Map to response items
	items := make([]OperationsHistoryItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, OperationsHistoryItem{
			ID:                     r.ID,
			ArtifactTechID:         r.ArtifactTechID,
			ArtifactName:           r.ArtifactName,
			ArtifactVersion:        r.ArtifactVersion,
			ArtifactType:           string(r.ArtifactType),
			PackageID:              r.PackageID,
			TenantID:               r.TenantID,
			TenantName:             r.TenantName,
			DeliveryRequestID:      r.DeliveryRequestID,
			DeliveryRequestName:    r.DeliveryRequestName,
			DeliveryRuleName:       r.DeliveryRuleName,
			TransportRequestNumber: r.TransportRequestNumber,
			RequestState:           string(r.RequestState),
			ImportState:            string(r.ImportState),
			DeployState:            string(r.DeployState),
			SkipDeploy:             r.SkipDeploy,
			LastError:              r.LastError,
			CreatedBy:              r.CreatedBy,
			UpdatedBy:              r.UpdatedBy,
			CreatedAt:              r.CreatedAt,
			UpdatedAt:              r.UpdatedAt,
		})
	}

	return OperationsHistoryResponse{
		Data:     items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// GetOperationsHistoryFilters returns available filter values for the UI.
func (s *Service) GetOperationsHistoryFilters() (OperationsHistoryFiltersResponse, error) {
	var resp OperationsHistoryFiltersResponse

	// Tenants
	var tenants []db.CpiTenant
	if err := s.DB.Select("id", "name").Find(&tenants).Error; err != nil {
		return resp, fmt.Errorf("failed to load tenants: %w", err)
	}
	for _, t := range tenants {
		resp.Tenants = append(resp.Tenants, FilterOption{ID: t.ID, Name: t.Name})
	}

	// Artifact types (distinct values from ops table)
	var types []string
	s.DB.Model(&db.ArtifactTenantOperation{}).
		Distinct("artifact_type").
		Where("artifact_type != ''").
		Pluck("artifact_type", &types)
	resp.ArtifactTypes = types

	// Delivery rules
	var rules []db.DeliveryRule
	if err := s.DB.Select("id", "name").Where("active = ?", true).Find(&rules).Error; err != nil {
		return resp, fmt.Errorf("failed to load delivery rules: %w", err)
	}
	for _, r := range rules {
		resp.DeliveryRules = append(resp.DeliveryRules, FilterOption{ID: r.ID, Name: r.Name})
	}

	// Operators (distinct created_by values)
	var operators []string
	s.DB.Model(&db.ArtifactTenantOperation{}).
		Distinct("created_by").
		Where("created_by != ''").
		Pluck("created_by", &operators)
	resp.Operators = operators

	return resp, nil
}

// allowedSortColumn whitelists sortable columns to prevent SQL injection.
func allowedSortColumn(col string) string {
	allowed := map[string]string{
		"created_at":    "artifact_tenant_operations.created_at",
		"updated_at":    "artifact_tenant_operations.updated_at",
		"artifact_name": "artifact_tenant_operations.artifact_name",
		"tenant_name":   "cpi_tenants.name",
	}
	if mapped, ok := allowed[col]; ok {
		return mapped
	}
	return "artifact_tenant_operations.updated_at"
}

// normalizePagination ensures page and pageSize are within valid bounds.
func normalizePagination(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	return page, pageSize
}

// GetOperationConditions returns the condition timeline for a specific operation.
func (s *Service) GetOperationConditions(opID uint) ([]db.Condition, error) {
	var conditions []db.Condition
	if err := s.DB.
		Where("artifact_tenant_operation_id = ?", opID).
		Order("created_at DESC").
		Find(&conditions).Error; err != nil {
		return nil, fmt.Errorf("failed to query conditions for op %d: %w", opID, err)
	}
	return conditions, nil
}
