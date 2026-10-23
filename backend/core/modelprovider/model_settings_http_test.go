package modelprovider

import (
 "encoding/json"
 "testing"
 "lazymind/core/common/orm"
)

func TestModelSettingsEditRoundTripAndLegacyRename(t *testing.T) {
 db, model := modelEditFixture(t, "llm", false)
 result := modelEditCall(t, model.ID, "user-1", `{"model_settings":{"feature_support":{"tool_calling":true,"reasoning":true},"feature_enabled":{"reasoning":false}}}`)
 if result.Code != 200 { t.Fatalf("settings edit=%d %s", result.Code,result.Body.String()) }
 var row orm.UserModelProviderGroupModel
 if err:=db.Take(&row,"id = ?",model.ID).Error; err!=nil { t.Fatal(err) }
 settings:=modelSettingsResponse(row.ModelSettings)
 if settings==nil || settings.FeatureSupport==nil || settings.FeatureSupport.ToolCalling==nil || !*settings.FeatureSupport.ToolCalling || settings.FeatureEnabled==nil || settings.FeatureEnabled.Reasoning==nil || *settings.FeatureEnabled.Reasoning { t.Fatalf("settings not persisted: %s",row.ModelSettings) }
 var payload struct{Data struct{ModelSettings *ModelSettings `json:"model_settings"`} `json:"data"`}
 if err:=json.Unmarshal(result.Body.Bytes(),&payload);err!=nil || payload.Data.ModelSettings==nil { t.Fatalf("missing response settings: %s",result.Body.String()) }
 result=modelEditCall(t,model.ID,"user-1",`{"name":"settings-renamed"}`)
 if result.Code!=200 { t.Fatalf("legacy rename=%d %s",result.Code,result.Body.String()) }
 var renamed orm.UserModelProviderGroupModel
 if err:=db.Take(&renamed,"id = ?",model.ID).Error;err!=nil || renamed.ModelSettings!=row.ModelSettings { t.Fatalf("legacy client lost metadata: %+v %v",renamed,err) }
}

func TestModelSettingsEditRejectsInvalidCapabilityCombination(t *testing.T) {
 _, model:=modelEditFixture(t,"llm",false)
 result:=modelEditCall(t,model.ID,"user-1",`{"model_settings":{"feature_support":{"tool_calling":false},"feature_enabled":{"tool_calling":true}}}`)
 if result.Code!=400 { t.Fatalf("accepted invalid settings=%d %s",result.Code,result.Body.String()) }
}

func TestEmbeddingModelSettingsEditKeepsWhitespace(t *testing.T) {
 db,model:=modelEditFixture(t,"embed",false)
 result:=modelEditCall(t,model.ID,"user-1",`{"model_settings":{"embedding":{"query_prefix":"query: ","passage_prefix":"passage:  ","batch_size":2}}}`)
 if result.Code!=200 {t.Fatalf("embedding settings=%d %s",result.Code,result.Body.String())}
 var row orm.UserModelProviderGroupModel
 if err:=db.Take(&row,"id = ?",model.ID).Error;err!=nil{t.Fatal(err)}
 settings:=modelSettingsResponse(row.ModelSettings)
 if settings==nil || settings.Embedding==nil || settings.Embedding.QueryPrefix!="query: " || settings.Embedding.PassagePrefix!="passage:  " {t.Fatalf("prefixes changed: %s",row.ModelSettings)}
}
