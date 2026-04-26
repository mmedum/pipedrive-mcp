package pipedrive

// Pipeline is a Pipedrive pipeline (a deal flow grouping). Subset of the
// /api/v2/pipelines response that we surface to LLM clients.
type Pipeline struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	OrderNr int    `json:"order_nr"`
	Active  bool   `json:"active"`
}

// Stage is a Pipedrive stage within a pipeline. Subset of /api/v2/stages.
type Stage struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	OrderNr         int    `json:"order_nr"`
	Active          bool   `json:"active_flag"`
	PipelineID      int64  `json:"pipeline_id"`
	DealProbability int    `json:"deal_probability"`
}
