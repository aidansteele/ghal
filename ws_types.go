package ghlogs

type consoleOutputMessage struct {
	InternalAzureRunId int      `json:"RunId"`
	TimelineId         string   `json:"timelineId"`
	TimelineRecordId   string   `json:"timelineRecordId"` // corresponds to Job's check run's "external_id"
	StepRecordId       string   `json:"stepRecordId"`
	StartLine          int      `json:"startLine"`
	Lines              []string `json:"lines"`
}

type stepProgressUpdate struct {
	InternalAzureRunId int    `json:"RunId"`
	TimelineId         string `json:"timelineId"`
	ParentRecordId     string `json:"parentRecordId"` // corresponds to Job's check run's "external_id"
	StepRecordId       string `json:"stepRecordId"`
	StepNumber         int64  `json:"stepNumber"`
	ChangeId           int    `json:"changeId"`
	StepCompleted      bool   `json:"stepCompleted"`
}
