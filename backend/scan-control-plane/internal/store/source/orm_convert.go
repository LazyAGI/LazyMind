package source

func sourceFromORM(row ormSource) Source {
	return Source{
		SourceID:          row.SourceID,
		TenantID:          row.TenantID,
		CreatedBy:         row.CreatedBy,
		Name:              row.Name,
		DatasetID:         row.DatasetID,
		Status:            row.Status,
		SourceOptions:     CloneJSON(row.SourceOptions),
		IncludeExtensions: CloneJSON(row.IncludeExtensions),
		ExcludeExtensions: CloneJSON(row.ExcludeExtensions),
		ConfigVersion:     row.ConfigVersion,
		DeletedAt:         row.DeletedAt,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
	}
}

func sourceToORM(src Source) ormSource {
	return ormSource{
		SourceID:          src.SourceID,
		TenantID:          src.TenantID,
		CreatedBy:         src.CreatedBy,
		Name:              src.Name,
		DatasetID:         src.DatasetID,
		Status:            src.Status,
		SourceOptions:     CloneJSON(src.SourceOptions),
		IncludeExtensions: CloneJSON(src.IncludeExtensions),
		ExcludeExtensions: CloneJSON(src.ExcludeExtensions),
		ConfigVersion:     src.ConfigVersion,
		DeletedAt:         src.DeletedAt,
		CreatedAt:         src.CreatedAt,
		UpdatedAt:         src.UpdatedAt,
	}
}

func bindingFromORM(row ormBinding) Binding {
	return Binding{
		BindingID:              row.BindingID,
		SourceID:               row.SourceID,
		BindingType:            row.BindingType,
		ConnectorType:          row.ConnectorType,
		TargetType:             row.TargetType,
		TargetRef:              row.TargetRef,
		TargetFingerprint:      row.TargetFingerprint,
		AgentID:                row.AgentID,
		AuthConnectionID:       row.AuthConnectionID,
		ProviderOptions:        CloneJSON(row.ProviderOptions),
		TreeKey:                row.TreeKey,
		BindingGeneration:      row.BindingGeneration,
		CoreParentDocumentID:   row.CoreParentDocumentID,
		CoreParentDocumentName: row.CoreParentDocumentName,
		SyncMode:               row.SyncMode,
		SchedulePolicy:         CloneJSON(row.SchedulePolicy),
		NextSyncAt:             row.NextSyncAt,
		IncludeExtensions:      CloneJSON(row.IncludeExtensions),
		ExcludeExtensions:      CloneJSON(row.ExcludeExtensions),
		Status:                 row.Status,
		LastError:              CloneJSON(row.LastError),
		DeletedAt:              row.DeletedAt,
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt,
	}
}

func bindingToORM(binding Binding) ormBinding {
	return ormBinding{
		BindingID:              binding.BindingID,
		SourceID:               binding.SourceID,
		BindingType:            binding.BindingType,
		ConnectorType:          binding.ConnectorType,
		TargetType:             binding.TargetType,
		TargetRef:              binding.TargetRef,
		TargetFingerprint:      binding.TargetFingerprint,
		AgentID:                binding.AgentID,
		AuthConnectionID:       binding.AuthConnectionID,
		ProviderOptions:        CloneJSON(binding.ProviderOptions),
		TreeKey:                binding.TreeKey,
		BindingGeneration:      binding.BindingGeneration,
		CoreParentDocumentID:   binding.CoreParentDocumentID,
		CoreParentDocumentName: binding.CoreParentDocumentName,
		SyncMode:               binding.SyncMode,
		SchedulePolicy:         CloneJSON(binding.SchedulePolicy),
		NextSyncAt:             binding.NextSyncAt,
		IncludeExtensions:      CloneJSON(binding.IncludeExtensions),
		ExcludeExtensions:      CloneJSON(binding.ExcludeExtensions),
		Status:                 binding.Status,
		LastError:              CloneJSON(binding.LastError),
		DeletedAt:              binding.DeletedAt,
		CreatedAt:              binding.CreatedAt,
		UpdatedAt:              binding.UpdatedAt,
	}
}

func objectFromORM(row ormSourceObject) SourceObject {
	return SourceObject{
		SourceID:        row.SourceID,
		BindingID:       row.BindingID,
		TreeKey:         row.TreeKey,
		ObjectKey:       row.ObjectKey,
		ParentKey:       row.ParentKey,
		DisplayName:     row.DisplayName,
		SearchName:      row.SearchName,
		ObjectType:      row.ObjectType,
		IsDocument:      row.IsDocument,
		IsContainer:     row.IsContainer,
		HasChildren:     row.HasChildren,
		SourceVersion:   row.SourceVersion,
		SizeBytes:       row.SizeBytes,
		MimeType:        row.MimeType,
		FileExtension:   row.FileExtension,
		ModifiedAt:      row.ModifiedAt,
		DeletedAtSource: row.DeletedAtSource,
		Depth:           row.Depth,
		ProviderMeta:    CloneJSON(row.ProviderMeta),
		LastSeenRunID:   row.LastSeenRunID,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}
}

func objectToORM(object SourceObject) ormSourceObject {
	return ormSourceObject{
		SourceID:        object.SourceID,
		BindingID:       object.BindingID,
		TreeKey:         object.TreeKey,
		ObjectKey:       object.ObjectKey,
		ParentKey:       object.ParentKey,
		DisplayName:     object.DisplayName,
		SearchName:      object.SearchName,
		ObjectType:      object.ObjectType,
		IsDocument:      object.IsDocument,
		IsContainer:     object.IsContainer,
		HasChildren:     object.HasChildren,
		SourceVersion:   object.SourceVersion,
		SizeBytes:       object.SizeBytes,
		MimeType:        object.MimeType,
		FileExtension:   object.FileExtension,
		ModifiedAt:      object.ModifiedAt,
		DeletedAtSource: object.DeletedAtSource,
		Depth:           object.Depth,
		ProviderMeta:    CloneJSON(object.ProviderMeta),
		LastSeenRunID:   object.LastSeenRunID,
		CreatedAt:       object.CreatedAt,
		UpdatedAt:       object.UpdatedAt,
	}
}

func documentStateFromORM(row ormDocumentState) DocumentState {
	return DocumentState{
		SourceID:            row.SourceID,
		BindingID:           row.BindingID,
		BindingGeneration:   row.BindingGeneration,
		ObjectKey:           row.ObjectKey,
		SourceVersion:       row.SourceVersion,
		BaselineVersion:     row.BaselineVersion,
		DeletedAtSource:     row.DeletedAtSource,
		SourceState:         row.SourceState,
		SyncState:           row.SyncState,
		PendingAction:       row.PendingAction,
		DocumentListVisible: row.DocumentListVisible,
		Selectable:          row.Selectable,
		ParseQueueState:     row.ParseQueueState,
		DocumentID:          row.DocumentID,
		ActiveTaskID:        row.ActiveTaskID,
		LastDetectedAt:      row.LastDetectedAt,
		LastSyncedAt:        row.LastSyncedAt,
		LastError:           CloneJSON(row.LastError),
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

func documentStateToORM(state DocumentState) ormDocumentState {
	return ormDocumentState{
		SourceID:            state.SourceID,
		BindingID:           state.BindingID,
		BindingGeneration:   state.BindingGeneration,
		ObjectKey:           state.ObjectKey,
		SourceVersion:       state.SourceVersion,
		BaselineVersion:     state.BaselineVersion,
		DeletedAtSource:     state.DeletedAtSource,
		SourceState:         state.SourceState,
		SyncState:           state.SyncState,
		PendingAction:       state.PendingAction,
		DocumentListVisible: state.DocumentListVisible,
		Selectable:          state.Selectable,
		ParseQueueState:     state.ParseQueueState,
		DocumentID:          state.DocumentID,
		ActiveTaskID:        state.ActiveTaskID,
		LastDetectedAt:      state.LastDetectedAt,
		LastSyncedAt:        state.LastSyncedAt,
		LastError:           CloneJSON(state.LastError),
		CreatedAt:           state.CreatedAt,
		UpdatedAt:           state.UpdatedAt,
	}
}

func documentFromORM(row ormDocument) Document {
	return Document{
		DocumentID:       row.DocumentID,
		TenantID:         row.TenantID,
		SourceID:         row.SourceID,
		BindingID:        row.BindingID,
		ObjectKey:        row.ObjectKey,
		CoreDocumentID:   row.CoreDocumentID,
		CurrentVersionID: row.CurrentVersionID,
		DesiredVersionID: row.DesiredVersionID,
		SourceVersion:    row.SourceVersion,
		DisplayName:      row.DisplayName,
		MimeType:         row.MimeType,
		FileExtension:    row.FileExtension,
		ParseStatus:      row.ParseStatus,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}
}

func documentToORM(document Document) ormDocument {
	return ormDocument{
		DocumentID:       document.DocumentID,
		TenantID:         document.TenantID,
		SourceID:         document.SourceID,
		BindingID:        document.BindingID,
		ObjectKey:        document.ObjectKey,
		CoreDocumentID:   document.CoreDocumentID,
		CurrentVersionID: document.CurrentVersionID,
		DesiredVersionID: document.DesiredVersionID,
		SourceVersion:    document.SourceVersion,
		DisplayName:      document.DisplayName,
		MimeType:         document.MimeType,
		FileExtension:    document.FileExtension,
		ParseStatus:      document.ParseStatus,
		CreatedAt:        document.CreatedAt,
		UpdatedAt:        document.UpdatedAt,
	}
}
