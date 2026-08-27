package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestScoreResearchOutput_PerfectDoc(t *testing.T) {
	// A well-structured research doc with all quality signals
	doc := `# Research: Multi-Agent Orchestration Patterns

## Methodology

Our approach uses a systematic evaluation framework to analyze orchestration patterns.
We employ a structured methodology for comparing different process models. The analysis
procedure examines each framework through a standardized evaluation rubric covering
reliability, scalability, and correctness. ` + strings.Repeat("Additional methodology context and detail for thorough coverage. ", 30) + `

## Analysis

` + strings.Repeat("Detailed analysis of orchestration framework patterns and their tradeoffs in distributed systems. ", 30) + `

| Framework | Reliability | Scalability | Correctness |
|-----------|------------|-------------|-------------|
| AutoGen   | High       | Medium      | High        |
| CrewAI    | Medium     | High        | Medium      |
| LangGraph | High       | High        | High        |

## Findings

` + strings.Repeat("We recommend adopting the conductor pattern for centralized orchestration and improved reliability. ", 25) + `

You should consider implementing circuit breakers. We suggest using topological sort
for merge ordering. The next step is to implement a monitoring dashboard. Action item:
add heartbeat checks. Consider migrating to event-driven architecture.

## Sources and References

` + strings.Repeat("Each source was evaluated for relevance and methodology quality. ", 15) + `

- https://arxiv.org/abs/2308.00352
- https://arxiv.org/abs/2402.01680
- https://arxiv.org/abs/2401.04088
- https://github.com/microsoft/autogen
- https://github.com/joaomdmoura/crewai
- https://docs.langchain.com/langgraph
- https://example.com/multi-agent-survey
- https://example.com/orchestration-patterns
- https://example.com/agent-coordination
- https://example.com/reliability-patterns
- https://example.com/scalability-analysis
- https://example.com/correctness-proofs
- https://example.com/monitoring-dashboard
- https://example.com/circuit-breakers
- https://example.com/heartbeat-protocol

## Limitations

There are several limitations and caveats to our analysis. The gap in our evaluation
is the lack of large-scale production data. These constraints should be considered
when applying our recommendations. ` + strings.Repeat("Further discussion of scope limitations and boundary conditions. ", 15) + `

## Next Steps

We recommend implementing the following action items to address identified gaps.
` + strings.Repeat("Detailed planning for next steps and future work in this research area. ", 15) + `
`

	score := ScoreResearchOutput(doc)

	if score.Methodology < 4 {
		t.Errorf("Methodology=%d, want >=4", score.Methodology)
	}
	if score.Sources < 4 {
		t.Errorf("Sources=%d, want >=4", score.Sources)
	}
	if score.Novelty < 3 {
		t.Errorf("Novelty=%d, want >=3", score.Novelty)
	}
	if score.Actionability < 4 {
		t.Errorf("Actionability=%d, want >=4", score.Actionability)
	}
	if score.Clarity < 4 {
		t.Errorf("Clarity=%d, want >=4", score.Clarity)
	}
	if score.Completeness < 4 {
		t.Errorf("Completeness=%d, want >=4", score.Completeness)
	}
	if !score.Pass {
		t.Errorf("Pass=false, want true (scores: M=%d S=%d N=%d A=%d Cl=%d Co=%d)",
			score.Methodology, score.Sources, score.Novelty, score.Actionability, score.Clarity, score.Completeness)
	}
}

func TestScoreResearchOutput_ZeroURLs(t *testing.T) {
	doc := `# Research Report

## Introduction

` + strings.Repeat("This is research content without any external references or links. ", 30) + `

## Methodology

Our approach uses a systematic framework and evaluation process.

## Findings

` + strings.Repeat("We found interesting results in our analysis. ", 20) + `

## Conclusion

Summary of findings.
`

	score := ScoreResearchOutput(doc)

	if score.Sources > 2 {
		t.Errorf("Sources=%d, want <=2 for doc with zero URLs", score.Sources)
	}
}

func TestScoreResearchOutput_ShortSectionsLowCompleteness(t *testing.T) {
	// Sections with <50 words each
	doc := `# Report

## Section 1

Short content.

## Section 2

Brief.

## Section 3

Tiny.

## Section 4

Small.

## Section 5

Minimal.
`

	score := ScoreResearchOutput(doc)

	if score.Completeness > 2 {
		t.Errorf("Completeness=%d, want <=2 for doc with short sections", score.Completeness)
	}
}

func TestScoreResearchOutput_FlatDocNoHeadings(t *testing.T) {
	// A flat document with no markdown headings
	doc := strings.Repeat("This is a flat document with no structure at all. ", 50) +
		"\nhttps://example.com/1\nhttps://example.com/2\nhttps://example.com/3\n"

	score := ScoreResearchOutput(doc)

	if score.Clarity > 2 {
		t.Errorf("Clarity=%d, want <=2 for flat doc with no headings", score.Clarity)
	}
}

func TestScoreResearchOutput_MissingLimitations(t *testing.T) {
	// Doc with good structure but no limitations/gaps/caveats
	doc := `# Report

## Introduction

` + strings.Repeat("Standard research content about important topics and findings. ", 20) + `

## Results

` + strings.Repeat("Detailed results and observations from our study. ", 20) + `

## Conclusion

` + strings.Repeat("Final summary and wrap up of the research findings. ", 10) + `
`

	score := ScoreResearchOutput(doc)

	// Without limitations, completeness should be lower than with them
	docWithLimitations := doc + "\n## Limitations\n\nThere are notable limitations and caveats to consider.\n"
	scoreWithLimitations := ScoreResearchOutput(docWithLimitations)

	if scoreWithLimitations.Completeness <= score.Completeness {
		t.Errorf("Completeness with limitations (%d) should be > without (%d)",
			scoreWithLimitations.Completeness, score.Completeness)
	}
}

func TestScoreResearchOutput_EmptyString(t *testing.T) {
	score := ScoreResearchOutput("")

	if score.Methodology != 1 {
		t.Errorf("Methodology=%d, want 1 for empty input", score.Methodology)
	}
	if score.Sources != 1 {
		t.Errorf("Sources=%d, want 1 for empty input", score.Sources)
	}
	if score.Novelty != 1 {
		t.Errorf("Novelty=%d, want 1 for empty input", score.Novelty)
	}
	if score.Actionability != 1 {
		t.Errorf("Actionability=%d, want 1 for empty input", score.Actionability)
	}
	if score.Clarity != 1 {
		t.Errorf("Clarity=%d, want 1 for empty input", score.Clarity)
	}
	if score.Completeness != 1 {
		t.Errorf("Completeness=%d, want 1 for empty input", score.Completeness)
	}
	if score.Overall != 1.0 {
		t.Errorf("Overall=%.2f, want 1.0 for empty input", score.Overall)
	}
	if score.Pass {
		t.Error("Pass=true, want false for empty input")
	}
}

func TestScoreResearchOutput_BoundaryAllThrees(t *testing.T) {
	// Construct a doc that hits at least 3 on each dimension:
	// Methodology>=3: needs 2 method keywords
	// Sources>=3: needs 5+ URLs
	// Novelty>=3: avg words per section >= 100
	// Actionability>=3: needs 3+ action patterns
	// Clarity>=3: needs 4+ headings
	// Completeness>=3: needs 60%+ good sections + limitations keyword
	doc := `# Research on Approach and Methodology

## Section 1

` + strings.Repeat("Word detail content analysis ", 30) + `

We recommend adopting this pattern. You should consider this carefully.
We suggest using an incremental approach.

https://example.com/1
https://example.com/2
https://example.com/3
https://example.com/4
https://example.com/5

## Section 2

` + strings.Repeat("Word detail content analysis ", 30) + `

There are some limitations to note.

## Section 3

` + strings.Repeat("Word detail content analysis ", 30) + `

## Section 4

` + strings.Repeat("Word detail content analysis ", 30) + `
`

	score := ScoreResearchOutput(doc)

	if score.Methodology < 3 {
		t.Errorf("Methodology=%d, want >=3", score.Methodology)
	}
	if score.Sources < 3 {
		t.Errorf("Sources=%d, want >=3", score.Sources)
	}
	if score.Novelty < 3 {
		t.Errorf("Novelty=%d, want >=3", score.Novelty)
	}
	if score.Actionability < 3 {
		t.Errorf("Actionability=%d, want >=3", score.Actionability)
	}
	if score.Clarity < 3 {
		t.Errorf("Clarity=%d, want >=3", score.Clarity)
	}
	if score.Completeness < 3 {
		t.Errorf("Completeness=%d, want >=3", score.Completeness)
	}
	if !score.Pass {
		t.Errorf("Pass=false, want true when all dimensions >=3 (M=%d S=%d N=%d A=%d Cl=%d Co=%d)",
			score.Methodology, score.Sources, score.Novelty, score.Actionability, score.Clarity, score.Completeness)
	}
}

func TestScoreResearchOutput_BoundaryOneDimFailsPass(t *testing.T) {
	// Same as above but strip URLs to get Sources=2, making Pass=false
	doc := `# Research on Approach and Methodology

## Section 1

` + strings.Repeat("Word ", 110) + `

We recommend adopting this pattern. You should consider this carefully.
We suggest using an incremental approach.

## Section 2

` + strings.Repeat("Word ", 110) + `

There are some limitations to note.

## Section 3

` + strings.Repeat("Word ", 110) + `

## Section 4

` + strings.Repeat("Word ", 110) + `
`

	score := ScoreResearchOutput(doc)

	if score.Sources >= 3 {
		t.Errorf("Sources=%d, want <3 for doc with no URLs", score.Sources)
	}
	if score.Pass {
		t.Errorf("Pass=true, want false when Sources=%d (<3)", score.Sources)
	}
}

func TestScoreResearchOutput_OverallIsAverage(t *testing.T) {
	doc := `# Test

## Section

Content here.
`

	score := ScoreResearchOutput(doc)

	expected := float64(score.Methodology+score.Sources+score.Novelty+score.Actionability+score.Clarity+score.Completeness) / 6.0
	if fmt.Sprintf("%.2f", score.Overall) != fmt.Sprintf("%.2f", expected) {
		t.Errorf("Overall=%.4f, want %.4f (average of dimensions)", score.Overall, expected)
	}
}

func TestScoreResearchOutput_TablesBoostClarity(t *testing.T) {
	docNoTables := `# Report

## Section 1

Content here.

## Section 2

More content.
`

	docWithTables := docNoTables + `
| Column A | Column B | Column C |
|----------|----------|----------|
| Data 1   | Data 2   | Data 3   |
`

	scoreNo := ScoreResearchOutput(docNoTables)
	scoreWith := ScoreResearchOutput(docWithTables)

	if scoreWith.Clarity < scoreNo.Clarity {
		t.Errorf("Tables should not decrease clarity: without=%d, with=%d", scoreNo.Clarity, scoreWith.Clarity)
	}
}

func TestScoreResearchOutput_ActionablePatterns(t *testing.T) {
	doc := `# Report

## Recommendations

We recommend implementing feature A. You should adopt pattern B.
We suggest migrating to approach C. The next step is to implement D.
Action item: review architecture. Consider using event sourcing.
You should implement monitoring. We recommend adding observability.
We suggest using circuit breakers. Consider adopting retry logic.

## Section

` + strings.Repeat("Content. ", 30) + `
`

	score := ScoreResearchOutput(doc)

	if score.Actionability < 4 {
		t.Errorf("Actionability=%d, want >=4 for doc with many recommendations", score.Actionability)
	}
}
