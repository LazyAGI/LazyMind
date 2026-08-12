package domain

import "gorm.io/gorm"

// DetectSchemaCapabilities probes the deployed database instead of assuming
// that an expand migration has run. Callers must gate host-neutral writes on
// this result so old and new binaries can overlap safely.
func DetectSchemaCapabilities(db *gorm.DB) SchemaCapabilities {
	if db == nil {
		return SchemaCapabilities{}
	}
	migrator := db.Migrator()
	return SchemaCapabilities{
		HostNeutralSessionRefs: migrator.HasTable("plugin_sessions") &&
			migrator.HasColumn("plugin_sessions", "origin_host") &&
			migrator.HasColumn("plugin_sessions", "origin_ref") &&
			migrator.HasColumn("plugin_sessions", "controller_host"),
	}
}
