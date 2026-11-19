// Package model is the schema of plg_volcengine as GORM models: the one
// description of its tables. migrations/*.sql stay what the host runs;
// `sub2api-plugin db diff` writes the next one from a change made here, and
// `sub2api-plugin db check` (run by pack) fails while the two disagree.
//
// Mind the defaults when creating a row from one of these structs: GORM
// writes a column's default for a zero field (an empty GroupType would be
// stored as AIGC).
package model

import "time"

// All lists every model, for sub2api-plugin db.
func All() []any { return []any{&AssetGroup{}, &Asset{}} }

// AssetGroup indexes an Ark asset group of an account (upstream is the
// truth; see migrations/0001_init.sql).
type AssetGroup struct {
	ID          int64      `gorm:"primaryKey;index:asset_groups_account_idx,priority:2"`
	AccountID   int64      `gorm:"not null;uniqueIndex:asset_groups_account_id_upstream_id_key,priority:1;index:asset_groups_account_idx,priority:1"`
	UpstreamID  string     `gorm:"not null;uniqueIndex:asset_groups_account_id_upstream_id_key,priority:2"`
	Name        string     `gorm:"not null;default:''"`
	Title       string     `gorm:"not null;default:''"`
	Description string     `gorm:"not null;default:''"`
	GroupType   string     `gorm:"not null;default:'AIGC'"`
	IndexStatus string     `gorm:"not null;default:'ok';check:asset_groups_index_status_check,index_status IN ('ok', 'missing')"`
	CreatedAt   time.Time  `gorm:"not null;default:now();autoCreateTime:false"`
	UpdatedAt   time.Time  `gorm:"not null;default:now();autoUpdateTime:false"`
	CheckedAt   *time.Time // when reconciliation last looked; NULL: never
}

func (AssetGroup) TableName() string { return "asset_groups" }

// Asset indexes an Ark asset; deleting its group deletes it (upstream
// deletes a group's assets with it).
type Asset struct {
	ID          int64       `gorm:"primaryKey;index:assets_group_idx,priority:2;index:assets_account_idx,priority:2"`
	AccountID   int64       `gorm:"not null;uniqueIndex:assets_account_id_upstream_id_key,priority:1;index:assets_account_idx,priority:1"`
	GroupID     int64       `gorm:"not null;index:assets_group_idx,priority:1"`
	Group       *AssetGroup `gorm:"foreignKey:GroupID;constraint:assets_group_id_fkey,OnDelete:CASCADE"`
	UpstreamID  string      `gorm:"not null;uniqueIndex:assets_account_id_upstream_id_key,priority:2"`
	Name        string      `gorm:"not null;default:''"`
	AssetType   string      `gorm:"not null;default:''"`
	URL         string      `gorm:"column:url;not null;default:''"`
	Status      string      `gorm:"not null;default:''"` // Ark's last reported status, unconstrained
	IndexStatus string      `gorm:"not null;default:'ok';check:assets_index_status_check,index_status IN ('ok', 'missing')"`
	CreatedAt   time.Time   `gorm:"not null;default:now();autoCreateTime:false"`
	UpdatedAt   time.Time   `gorm:"not null;default:now();autoUpdateTime:false"`
	CheckedAt   *time.Time
}

func (Asset) TableName() string { return "assets" }
