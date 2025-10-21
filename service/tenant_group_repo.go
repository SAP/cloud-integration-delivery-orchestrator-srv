package service

import (
	"context"
	"mmt-delivery/db"

	"gorm.io/gorm"
)

type TenantGroupRepo interface {
	GetByID(ctx context.Context, id uint) (*db.TenantGroup, error)
	Create(ctx context.Context, group *db.TenantGroup) error
	Update(ctx context.Context, group *db.TenantGroup) error
	List(ctx context.Context) ([]db.TenantGroup, error)
	Delete(ctx context.Context, id uint) error
	AddTenants(ctx context.Context, groupID uint, tenantIDs []uint) error
	RemoveTenants(ctx context.Context, groupID uint, tenantIDs []uint) error
}

type tenantGroupRepo struct{}

func NewTenantGroupRepo() *tenantGroupRepo { return &tenantGroupRepo{} }

func (r *tenantGroupRepo) Create(ctx context.Context, g *db.TenantGroup, user string) error {
	g.CreatedBy, g.UpdatedBy = user, user
	return db.Conn().Session(&gorm.Session{FullSaveAssociations: true}).Create(&g).Error
}

func (r *tenantGroupRepo) GetByID(ctx context.Context, id uint) (g *db.TenantGroup, err error) {
	err = db.Conn().Preload("Tenants").First(&g, id).Error
	return
}

func (r *tenantGroupRepo) Update(ctx context.Context, g *db.TenantGroup, user string) error {
	g.CreatedBy, g.UpdatedBy = user, user
	return db.Conn().Session(&gorm.Session{FullSaveAssociations: true}).Updates(&g).Error
}

func (r *tenantGroupRepo) List(ctx context.Context) (groups []db.TenantGroup, err error) {
	err = db.Conn().Preload("Tenants").Find(&groups).Error
	return
}

func (r *tenantGroupRepo) Delete(ctx context.Context, id uint) error {
	return db.Conn().Delete(&db.TenantGroup{}, id).Error
}

func (r *tenantGroupRepo) AddTenants(ctx context.Context, groupID uint, tenantIDs []uint) error {
	var group db.TenantGroup
	if err := db.Conn().Preload("Tenants").First(&group, groupID).Error; err != nil {
		return err
	}
	var tenants []db.CpiTenant
	if err := db.Conn().Find(&tenants, tenantIDs).Error; err != nil {
		return err
	}
	return db.Conn().Model(&group).Association("Tenants").Append(&tenants)
}

func (r *tenantGroupRepo) RemoveTenants(ctx context.Context, groupID uint, tenantIDs []uint) error {
	var group db.TenantGroup
	if err := db.Conn().Preload("Tenants").First(&group, groupID).Error; err != nil {
		return err
	}
	var tenants []db.CpiTenant
	if err := db.Conn().Find(&tenants, tenantIDs).Error; err != nil {
		return err
	}
	return db.Conn().Model(&group).Association("Tenants").Delete(&tenants)
}