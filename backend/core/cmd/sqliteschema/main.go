// Command sqliteschema prints a deterministic SQLite DDL snapshot for migrations.
// It is a development-time generator; production startup never invokes AutoMigrate.
package main

import (
	"fmt"
	"os"
	"sort"

	"lazymind/core/common/orm"
)

func main() {
	dsn := "file:sqlite-schema-generator?mode=memory&cache=shared"
	db, err := orm.Connect(orm.DriverSQLite, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := db.AutoMigrate(orm.AllModelsForDDL()...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	type object struct {
		Type string
		Name string
		SQL  string
	}
	var objects []object
	if err := db.Raw(`SELECT type, name, sql FROM sqlite_master
WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'
ORDER BY CASE type WHEN 'table' THEN 0 ELSE 1 END, name`).Scan(&objects).Error; err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sort.SliceStable(objects, func(i, j int) bool {
		if objects[i].Type != objects[j].Type {
			return objects[i].Type == "table"
		}
		return objects[i].Name < objects[j].Name
	})
	for _, item := range objects {
		fmt.Printf("%s;\n\n", item.SQL)
	}
}
