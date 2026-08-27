package priority

import "strings"

// ClassifyWorkItem applies the ethical monetization framework to a work item.
// Hierarchy (highest priority rule wins):
//  1. Grant-funded → check grant type for OS requirement
//  2. Ethical imperative (replacing exploitative pricing) → open-source-free
//  3. Enterprise tools → proprietary, monetize
//  4. Individual tools → open-source
//  5. Developing country use → free
//  6. Default → undecided (routes to decision queue)
func ClassifyWorkItem(item *WorkItem) {
	if item.LicenseType != "" && item.LicenseType != "undecided" {
		return // already classified
	}

	title := strings.ToLower(item.Title)
	desc := strings.ToLower(item.Description)
	combined := title + " " + desc

	// Rule 1: Grant-funded items
	if item.Source == SourceGrant {
		classifyGrant(item)
		return
	}

	// Rule 2: Ethical imperative — replacing exploitative pricing for individuals
	if isEthicalImperative(combined) {
		item.LicenseType = "open-source-free"
		item.LicenseReason = "ethical: replacing exploitative pricing for individuals"
		return
	}

	// Rule 3: Enterprise tools
	if isEnterprise(combined) {
		item.LicenseType = "proprietary"
		item.LicenseReason = "enterprise monetization"
		return
	}

	// Rule 4: Individual tools
	if isIndividualTool(combined) {
		item.LicenseType = "open-source"
		item.LicenseReason = "individual/community use"
		return
	}

	// Rule 6: Default
	item.LicenseType = "undecided"
	item.LicenseReason = "needs human classification"
}

// classifyGrant determines license based on grant program type.
func classifyGrant(item *WorkItem) {
	desc := strings.ToLower(item.Description + " " + item.Title)

	switch {
	case strings.Contains(desc, "nsf pose") || strings.Contains(desc, "pathways to open-source"):
		item.LicenseType = "open-source"
		item.LicenseReason = "grant requires open source (NSF POSE)"
	case strings.Contains(desc, "sbir") || strings.Contains(desc, "small business innovation"):
		item.LicenseType = "proprietary-ok"
		item.LicenseReason = "grant allows proprietary (SBIR)"
	case strings.Contains(desc, "nih"):
		item.LicenseType = "open-source"
		item.LicenseReason = "NIH data sharing policy — default open"
	default:
		item.LicenseType = "open-source"
		item.LicenseReason = "grant-funded — default open source"
	}
}

// ethicalDomains are areas where companies exploit individuals with high prices.
var ethicalDomains = []string{
	"healthcare", "health record", "medical", "patient",
	"education", "teacher", "student", "iep", "special education",
	"mental health", "cbt", "therapy", "counseling",
	"financial literacy", "tax", "debt", "benefits",
	"immigration", "visa",
	"accessibility", "adhd", "autism", "dyslexia",
	"elder care", "caregiver",
	"language learning",
}

// ethicalPriceSignals indicate a tool replaces expensive commercial alternatives.
var ethicalPriceSignals = []string{
	"$50", "$100", "$200", "$500", "$1000", "$1,000",
	"/mo", "/yr", "/year", "/month",
	"per user", "per seat", "per student",
	"paywall", "subscription",
}

func isEthicalImperative(text string) bool {
	for _, domain := range ethicalDomains {
		if strings.Contains(text, domain) {
			return true
		}
	}
	for _, signal := range ethicalPriceSignals {
		if strings.Contains(text, signal) {
			return true
		}
	}
	return false
}

func isEnterprise(text string) bool {
	keywords := []string{
		"enterprise", "b2b", "saas platform", "team management",
		"org-wide", "company-wide", "corporate",
		"sso", "audit log", "rbac", "compliance dashboard",
	}
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func isIndividualTool(text string) bool {
	keywords := []string{
		"developer", "open-source", "open source", "personal",
		"self-hosted", "local-first", "offline",
		"hobby", "creative", "freelancer",
	}
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

// DetermineExecutionMode returns the execution mode for a work item based on
// its source, roles, and tier.
func DetermineExecutionMode(item *WorkItem) string {
	// Research-only items
	if len(item.AgentRoles) == 1 && item.AgentRoles[0] == "researcher" {
		return "research"
	}

	// Grant applications
	if item.Source == SourceGrant {
		return "grant"
	}

	// New projects need research first
	if item.IsNewProject {
		return "research"
	}

	// Default: multi-agent conductor
	return "conduct"
}
