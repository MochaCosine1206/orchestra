package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/MochaCosine1206/orchestra/internal/agent"
)

// ResearchPlan is the structured output from the PLAN step.
type ResearchPlan struct {
	SubQuestions []SubQuestion `json:"sub_questions"`
}

// SubQuestion represents a single research sub-question.
type SubQuestion struct {
	ID               string `json:"id"`
	Question         string `json:"question"`
	CompletionSignal string `json:"completion_signal"`
}

// SearchRound is the structured output from each SEARCH step.
type SearchRound struct {
	RoundNumber int           `json:"round_number"`
	Topics      []TopicResult `json:"topics"`
	Metrics     RoundMetrics  `json:"metrics"`
}

// TopicResult holds results for a single sub-question in a search round.
type TopicResult struct {
	QuestionID       string   `json:"question_id"`
	QueriesExecuted  []string `json:"queries_executed"`
	Claims           []Claim  `json:"claims"`
	SourcesThisRound int      `json:"sources_this_round"`
	NewClaimsCount   int      `json:"new_claims_this_round"`
	TotalClaims      int      `json:"total_claims"`
	Status           string   `json:"status"` // "exploring", "saturated", "gap"
}

// Claim represents a single factual assertion with its source.
type Claim struct {
	Text           string `json:"text"`
	SourceURL      string `json:"source_url"`
	SourceTitle    string `json:"source_title"`
	SourceYear     string `json:"source_year"`
	Classification string `json:"classification"` // "new", "duplicate", "conflict"
}

// RoundMetrics holds saturation metrics computed by Go code.
type RoundMetrics struct {
	NewClaimRate        float64 `json:"new_claim_rate"`
	ConsecutiveLowYield int     `json:"consecutive_low_yield"`
	TopicCoverage       float64 `json:"topic_coverage"`
	SourceDiversity     float64 `json:"source_diversity"`
}

// SynthesisResult is the structured output from the SYNTHESIZE step.
type SynthesisResult struct {
	Document         string           `json:"document"`
	SaturationReport SaturationReport `json:"saturation_report"`
	Gaps             []string         `json:"gaps"`
}

// SaturationReport summarizes the research loop's saturation state.
type SaturationReport struct {
	RoundsCompleted int          `json:"rounds_completed"`
	TotalSources    int          `json:"total_sources"`
	UniqueDomains   int          `json:"unique_domains"`
	TotalClaims     int          `json:"total_claims"`
	FinalMetrics    RoundMetrics `json:"final_metrics"`
}

// ResearchLoopConfig holds tunable parameters.
type ResearchLoopConfig struct {
	MinRounds              int
	MaxRounds              int
	SaturationThreshold    float64
	ConsecutiveLowYieldReq int
	TopicCoverageReq       float64
	SourceDiversityReq     float64
}

// DefaultResearchLoopConfig returns the default configuration for the research loop.
func DefaultResearchLoopConfig() ResearchLoopConfig {
	return ResearchLoopConfig{
		MinRounds:              3,
		MaxRounds:              8,
		SaturationThreshold:    0.15,
		ConsecutiveLowYieldReq: 2,
		TopicCoverageReq:       0.85,
		SourceDiversityReq:     0.5,
	}
}

// ResearchLoopResult captures the outcome of a research loop.
type ResearchLoopResult struct {
	Plan             ResearchPlan
	Rounds           []SearchRound
	AllClaims        []Claim
	Synthesis        SynthesisResult
	RoundsCompleted  int
	TotalClaims      int
	SaturationReport SaturationReport
	Gaps             []string
}

// --- JSON Schemas ---

const researchPlanSchema = `{
  "type": "object",
  "properties": {
    "sub_questions": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "question": { "type": "string" },
          "completion_signal": { "type": "string" }
        },
        "required": ["id", "question", "completion_signal"]
      },
      "minItems": 3,
      "maxItems": 8
    }
  },
  "required": ["sub_questions"]
}`

const searchRoundSchema = `{
  "type": "object",
  "properties": {
    "round_number": { "type": "integer" },
    "topics": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "question_id": { "type": "string" },
          "queries_executed": { "type": "array", "items": { "type": "string" } },
          "claims": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "text": { "type": "string" },
                "source_url": { "type": "string" },
                "source_title": { "type": "string" },
                "source_year": { "type": "string" },
                "classification": { "type": "string", "enum": ["new", "duplicate", "conflict"] }
              },
              "required": ["text", "source_url", "classification"]
            }
          },
          "sources_this_round": { "type": "integer" },
          "new_claims_this_round": { "type": "integer" },
          "total_claims": { "type": "integer" },
          "status": { "type": "string", "enum": ["exploring", "saturated", "gap"] }
        },
        "required": ["question_id", "queries_executed", "claims", "sources_this_round", "new_claims_this_round", "total_claims", "status"]
      }
    },
    "metrics": {
      "type": "object",
      "properties": {
        "new_claim_rate": { "type": "number" },
        "consecutive_low_yield": { "type": "integer" },
        "topic_coverage": { "type": "number" },
        "source_diversity": { "type": "number" }
      },
      "required": ["new_claim_rate", "consecutive_low_yield", "topic_coverage", "source_diversity"]
    }
  },
  "required": ["round_number", "topics", "metrics"]
}`

const synthesisSchema = `{
  "type": "object",
  "properties": {
    "document": { "type": "string", "description": "Full markdown document with all sections" },
    "saturation_report": {
      "type": "object",
      "properties": {
        "rounds_completed": { "type": "integer" },
        "total_sources": { "type": "integer" },
        "unique_domains": { "type": "integer" },
        "total_claims": { "type": "integer" },
        "final_metrics": {
          "type": "object",
          "properties": {
            "new_claim_rate": { "type": "number" },
            "consecutive_low_yield": { "type": "integer" },
            "topic_coverage": { "type": "number" },
            "source_diversity": { "type": "number" }
          }
        }
      },
      "required": ["rounds_completed", "total_sources", "unique_domains", "total_claims"]
    },
    "gaps": { "type": "array", "items": { "type": "string" } }
  },
  "required": ["document", "saturation_report", "gaps"]
}`

// --- Prompt Templates ---

const planPrompt = `You are planning a deep research investigation. Analyze the task below and decompose it into 3-8 specific sub-questions that, when answered comprehensively, will satisfy the acceptance criteria.

## TASK
%s

## ACCEPTANCE CRITERIA
%s

For each sub-question, define what a complete answer looks like (the completion signal).`

const searchRoundPrompt = `You are conducting research round %d of a deep investigation. Your job is to search for information, extract factual claims, and classify each claim as new, duplicate, or conflict.

## RESEARCH PLAN
%s

## ACCUMULATED CLAIMS FROM PRIOR ROUNDS
%s

## INSTRUCTIONS FOR THIS ROUND
Search for information on the topics that are still marked "exploring" or "gap". For each topic:
1. Execute 3-5 web searches with varied keywords
2. Read the most promising results
3. Extract atomic claims — one factual assertion per claim, with source URL
4. Classify each claim:
   - "new" if this information was NOT in the accumulated claims
   - "duplicate" if it restates something already found
   - "conflict" if it contradicts something already found

Report metrics honestly. Do not inflate new-claim counts.`

const synthesisPrompt = `You are synthesizing research findings into a comprehensive document. You have completed %d search rounds and accumulated %d unique claims across %d topics.

## TASK
%s

## ACCEPTANCE CRITERIA
%s

## ALL ACCUMULATED CLAIMS
%s

## RESEARCH PLAN
%s

Write a comprehensive research document in markdown. Organize by topic, not by source. Every claim must cite its source. Include:
- A section for each sub-question from the research plan
- A "## Gaps and Limitations" section for what you could NOT find
- An "## Implications for the Novel" section connecting findings to the project
- A "## Sources" section with all citations

The document should be thorough, well-structured, and ready to inform downstream decision-making.`

// --- Core Loop ---

// ResearchLoop executes the recursive research loop for a deep-researcher task.
// It replaces the single claude -p call with multiple structured LLM calls:
// PLAN -> N x SEARCH -> SYNTHESIZE.
//
// The LLM searches and classifies claims. Go code evaluates saturation metrics
// and decides whether to continue. The LLM never decides when to stop.
func (c *Conductor) ResearchLoop(ctx context.Context, taskDesc string, taskAC string, outputFiles []string, config ResearchLoopConfig) (*ResearchLoopResult, error) {
	// Step 1: PLAN
	c.log("Research loop: starting plan step")
	prompt := fmt.Sprintf(planPrompt, taskDesc, taskAC)

	result, err := c.Runner.Run(ctx, RunOpts{
		Prompt:     prompt,
		Model:      agent.ModelOpus,
		JSONSchema: researchPlanSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("research plan: %w", err)
	}

	var plan ResearchPlan
	if err := json.Unmarshal([]byte(result.Output), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal research plan: %w", err)
	}
	c.log("Research loop: plan produced %d sub-questions", len(plan.SubQuestions))

	// Step 2: SEARCH ROUNDS
	var allClaims []Claim
	var rounds []SearchRound
	consecutiveLow := 0

	for round := 1; round <= config.MaxRounds; round++ {
		// Build claims context — after round 5, summarize instead of full JSON
		claimsContext, err := buildClaimsContext(allClaims, round)
		if err != nil {
			return nil, fmt.Errorf("building claims context for round %d: %w", round, err)
		}

		planJSON, err := json.Marshal(plan)
		if err != nil {
			return nil, fmt.Errorf("marshal plan for round %d: %w", round, err)
		}

		roundPrompt := fmt.Sprintf(searchRoundPrompt, round, string(planJSON), claimsContext)

		c.log("Research loop: starting search round %d", round)
		roundResult, err := c.Runner.Run(ctx, RunOpts{
			Prompt:     roundPrompt,
			Model:      agent.ModelOpus,
			JSONSchema: searchRoundSchema,
		})
		if err != nil {
			return nil, fmt.Errorf("research search round %d: %w", round, err)
		}

		var searchRound SearchRound
		if err := json.Unmarshal([]byte(roundResult.Output), &searchRound); err != nil {
			return nil, fmt.Errorf("unmarshal search round %d: %w", round, err)
		}
		rounds = append(rounds, searchRound)

		// Accumulate new claims
		for _, topic := range searchRound.Topics {
			for _, claim := range topic.Claims {
				if claim.Classification == "new" {
					allClaims = append(allClaims, claim)
				}
			}
		}

		// Go code evaluates metrics (NOT the LLM)
		metrics := computeMetrics(searchRound, allClaims, plan)

		// Track consecutive low-yield
		if metrics.NewClaimRate < config.SaturationThreshold {
			consecutiveLow++
		} else {
			consecutiveLow = 0
		}
		metrics.ConsecutiveLowYield = consecutiveLow

		// Log round
		_ = c.DB.LogEvent(ctx, "research_round", "", "",
			fmt.Sprintf(`{"round":%d,"new_claim_rate":%.2f,"consecutive_low":%d,"topic_coverage":%.2f,"total_claims":%d}`,
				round, metrics.NewClaimRate, consecutiveLow, metrics.TopicCoverage, len(allClaims)))

		c.log("Research round %d: %.0f%% new-claim rate, %d consecutive low-yield, %.0f%% topic coverage, %d total claims",
			round, metrics.NewClaimRate*100, consecutiveLow, metrics.TopicCoverage*100, len(allClaims))

		// Saturation check (Go code decides, not LLM)
		if isSaturated(metrics, round, config) {
			c.log("Saturation reached at round %d", round)
			break
		}
	}

	// Step 3: SYNTHESIZE
	c.log("Research loop: starting synthesis step (%d rounds, %d claims)", len(rounds), len(allClaims))

	claimsJSON, err := json.Marshal(allClaims)
	if err != nil {
		return nil, fmt.Errorf("marshal claims for synthesis: %w", err)
	}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("marshal plan for synthesis: %w", err)
	}

	synthPrompt := fmt.Sprintf(synthesisPrompt,
		len(rounds), len(allClaims), len(plan.SubQuestions),
		taskDesc, taskAC,
		string(claimsJSON), string(planJSON),
	)

	synthResult, err := c.Runner.Run(ctx, RunOpts{
		Prompt:     synthPrompt,
		Model:      agent.ModelOpus,
		JSONSchema: synthesisSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("research synthesis: %w", err)
	}

	var synthesis SynthesisResult
	if err := json.Unmarshal([]byte(synthResult.Output), &synthesis); err != nil {
		return nil, fmt.Errorf("unmarshal synthesis: %w", err)
	}

	c.log("Research loop: synthesis complete, document=%d bytes, gaps=%d",
		len(synthesis.Document), len(synthesis.Gaps))

	return &ResearchLoopResult{
		Plan:             plan,
		Rounds:           rounds,
		AllClaims:        allClaims,
		Synthesis:        synthesis,
		RoundsCompleted:  len(rounds),
		TotalClaims:      len(allClaims),
		SaturationReport: synthesis.SaturationReport,
		Gaps:             synthesis.Gaps,
	}, nil
}

// computeMetrics calculates saturation metrics from Go code, not LLM judgment.
func computeMetrics(round SearchRound, allClaims []Claim, plan ResearchPlan) RoundMetrics {
	// Count new claims this round
	totalExtracted := 0
	newCount := 0
	for _, topic := range round.Topics {
		for _, claim := range topic.Claims {
			totalExtracted++
			if claim.Classification == "new" {
				newCount++
			}
		}
	}

	newClaimRate := 0.0
	if totalExtracted > 0 {
		newClaimRate = float64(newCount) / float64(totalExtracted)
	}

	// Topic coverage: topics with 3+ total claims
	topicsWithSources := 0
	for _, topic := range round.Topics {
		if topic.TotalClaims >= 3 {
			topicsWithSources++
		}
	}
	topicCoverage := 0.0
	if len(plan.SubQuestions) > 0 {
		topicCoverage = float64(topicsWithSources) / float64(len(plan.SubQuestions))
	}

	// Source diversity: unique domains across all accumulated claims
	domains := make(map[string]bool)
	totalSources := len(allClaims)
	for _, claim := range allClaims {
		if u, err := url.Parse(claim.SourceURL); err == nil && u.Host != "" {
			domains[u.Host] = true
		}
	}
	sourceDiversity := 0.0
	if totalSources > 0 {
		sourceDiversity = float64(len(domains)) / float64(totalSources)
	}

	return RoundMetrics{
		NewClaimRate:    newClaimRate,
		TopicCoverage:   topicCoverage,
		SourceDiversity: sourceDiversity,
	}
}

// isSaturated checks whether the research loop should stop based on Go-computed metrics.
func isSaturated(metrics RoundMetrics, round int, config ResearchLoopConfig) bool {
	if round < config.MinRounds {
		return false
	}
	return metrics.NewClaimRate < config.SaturationThreshold &&
		metrics.ConsecutiveLowYield >= config.ConsecutiveLowYieldReq &&
		metrics.TopicCoverage >= config.TopicCoverageReq
}

// buildClaimsContext returns the claims as JSON for injection into search round prompts.
// After round 5, it summarizes claims instead of passing the full JSON to manage context size.
func buildClaimsContext(claims []Claim, round int) (string, error) {
	if len(claims) == 0 {
		return "[]", nil
	}

	if round <= 5 {
		data, err := json.Marshal(claims)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	// After round 5: summarize by source domain to reduce context size
	type domainSummary struct {
		Domain     string   `json:"domain"`
		ClaimCount int      `json:"claim_count"`
		Topics     []string `json:"sample_claims"`
	}

	byDomain := make(map[string]*domainSummary)
	for _, claim := range claims {
		host := "unknown"
		if u, err := url.Parse(claim.SourceURL); err == nil && u.Host != "" {
			host = u.Host
		}
		ds, ok := byDomain[host]
		if !ok {
			ds = &domainSummary{Domain: host}
			byDomain[host] = ds
		}
		ds.ClaimCount++
		// Keep up to 3 sample claims per domain
		if len(ds.Topics) < 3 {
			ds.Topics = append(ds.Topics, claim.Text)
		}
	}

	summaries := make([]*domainSummary, 0, len(byDomain))
	for _, ds := range byDomain {
		summaries = append(summaries, ds)
	}

	wrapper := struct {
		TotalClaims int              `json:"total_claims"`
		Note        string           `json:"note"`
		ByDomain    []*domainSummary `json:"by_domain"`
	}{
		TotalClaims: len(claims),
		Note:        "Summarized to reduce context. Full claims available internally. Focus on finding NEW information not covered by these domains/topics.",
		ByDomain:    summaries,
	}

	data, err := json.Marshal(wrapper)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
