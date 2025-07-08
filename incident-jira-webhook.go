package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Configuration
type Config struct {
	JiraBaseURL                    string
	JiraUsername                   string
	JiraAPIToken                   string
	IncidentAPIToken               string
	WebhookSecret                  string
	Port                          string
	JiraWorkspaceID               string
	ImpactedComponentFieldName    string
	ImpactedComponentJiraFieldID  string
	ResponsibleComponentFieldName string
	ResponsibleComponentJiraFieldID string
}

// Field mappings
type FieldMapping struct {
	IncidentFieldName string `json:"incident_field_name"`
	JiraFieldID       string `json:"jira_field_id"`
}

// getFieldMappings returns field mappings from config
func (s *IncidentJiraSync) getFieldMappings() map[string]FieldMapping {
	return map[string]FieldMapping{
		"impacted_components": {
			IncidentFieldName: s.config.ImpactedComponentFieldName,
			JiraFieldID:       s.config.ImpactedComponentJiraFieldID,
		},
		"responsible_components": {
			IncidentFieldName: s.config.ResponsibleComponentFieldName,
			JiraFieldID:       s.config.ResponsibleComponentJiraFieldID,
		},
	}
}

// Incident.io API structures
type IncidentData struct {
	Incident struct {
		ID                       string `json:"id"`
		Name                     string `json:"name"`
		ExternalIssueReference   ExternalIssueReference `json:"external_issue_reference"`
		CustomFieldEntries       []CustomFieldEntry     `json:"custom_field_entries"`
	} `json:"incident"`
	PublicIncidentUpdatedV2 struct {
		ID                       string `json:"id"`
		Name                     string `json:"name"`
		ExternalIssueReference   ExternalIssueReference `json:"external_issue_reference"`
		CustomFieldEntries       []CustomFieldEntry     `json:"custom_field_entries"`
	} `json:"public_incident.incident_updated_v2"`
	EventType string `json:"event_type"`
}

type ExternalIssueReference struct {
	Provider       string `json:"provider"`
	IssueName      string `json:"issue_name"`
	IssuePermalink string `json:"issue_permalink"`
}

type CustomFieldEntry struct {
	CustomField CustomField `json:"custom_field"`
	Values      []Value     `json:"values"`
}

type CustomField struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FieldType   string `json:"field_type"`
}

type Value struct {
	ValueCatalogEntry *CatalogEntry `json:"value_catalog_entry,omitempty"`
}

type CatalogEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ExternalID string `json:"external_id"`
}

// Catalog API response
type CatalogResponse struct {
	CatalogEntry struct {
		ID              string                         `json:"id"`
		Name            string                         `json:"name"`
		ExternalID      string                         `json:"external_id"`
		AttributeValues map[string]AttributeValue      `json:"attribute_values"`
	} `json:"catalog_entry"`
	CatalogType struct {
		Schema struct {
			Attributes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"attributes"`
		} `json:"schema"`
	} `json:"catalog_type"`
}

type AttributeValue struct {
	Value struct {
		Literal string `json:"literal"`
	} `json:"value"`
}

// Jira API structures
type JiraUpdateRequest struct {
	Fields map[string]interface{} `json:"fields"`
}

type JiraComponentValue struct {
	ID       string `json:"id"`
	ObjectID string `json:"objectId"`
}

// IncidentJiraSync handles the synchronization logic
type IncidentJiraSync struct {
	config Config
	client *http.Client
}

func NewIncidentJiraSync(config Config) *IncidentJiraSync {
	// Create HTTP client with TLS config
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	
	return &IncidentJiraSync{
		config: config,
		client: &http.Client{Transport: tr},
	}
}

// getCatalogEntryObjectKey fetches catalog entry from incident.io API to get the object key attribute
func (s *IncidentJiraSync) getCatalogEntryObjectKey(catalogEntryID string) (string, error) {
	url := fmt.Sprintf("https://api.incident.io/v2/catalog_entries/%s", catalogEntryID)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", s.config.IncidentAPIToken))
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch catalog entry: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}
	
	var catalogResp CatalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&catalogResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	
	// Look for object key in the catalog entry's attributes
	// First, find the attribute ID for "object key"
	var objectKeyAttrID string
	for _, attr := range catalogResp.CatalogType.Schema.Attributes {
		if strings.ToLower(attr.Name) == "object key" {
			objectKeyAttrID = attr.ID
			break
		}
	}
	
	if objectKeyAttrID != "" {
		if attrValue, exists := catalogResp.CatalogEntry.AttributeValues[objectKeyAttrID]; exists {
			objectKey := attrValue.Value.Literal
			log.Printf("Found object key '%s' for catalog entry %s", objectKey, catalogEntryID)
			return objectKey, nil
		}
	}
	
	log.Printf("No object key found for catalog entry %s", catalogEntryID)
	return "", fmt.Errorf("no object key found for catalog entry %s", catalogEntryID)
}

// extractJiraObjectID extracts the numeric ID from object key (e.g., 'PIN-3' -> '3')
func (s *IncidentJiraSync) extractJiraObjectID(objectKey string) (string, error) {
	if objectKey == "" {
		return "", fmt.Errorf("empty object key")
	}
	
	// Extract number from formats like 'PIN-3', 'SUP-10', etc.
	re := regexp.MustCompile(`-(\d+)$`)
	matches := re.FindStringSubmatch(objectKey)
	if len(matches) > 1 {
		return matches[1], nil
	}
	
	// If it's already just a number
	if _, err := strconv.Atoi(objectKey); err == nil {
		return objectKey, nil
	}
	
	return "", fmt.Errorf("could not extract numeric ID from object key: %s", objectKey)
}

// formatJiraComponentValue formats component value for Jira API
func (s *IncidentJiraSync) formatJiraComponentValue(objectID, catalogEntryID string) JiraComponentValue {
	return JiraComponentValue{
		ID:       fmt.Sprintf("%s:%s", s.config.JiraWorkspaceID, objectID),
		ObjectID: objectID,
	}
}

// updateJiraCustomField updates a custom field in Jira with the provided values
func (s *IncidentJiraSync) updateJiraCustomField(jiraIssueKey, fieldID string, values []JiraComponentValue) error {
	// Create HTTP client for Jira API request
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	
	url := fmt.Sprintf("%s/rest/api/3/issue/%s", s.config.JiraBaseURL, jiraIssueKey)
	
	// Convert values to interface{} for JSON marshaling
	interfaceValues := make([]interface{}, len(values))
	for i, v := range values {
		interfaceValues[i] = v
	}
	
	payload := JiraUpdateRequest{
		Fields: map[string]interface{}{
			fieldID: interfaceValues,
		},
	}
	
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	
	log.Printf("Updating Jira %s with payload: %s", jiraIssueKey, string(payloadBytes))
	
	req, err := http.NewRequest("PUT", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	req.SetBasicAuth(s.config.JiraUsername, s.config.JiraAPIToken)
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update Jira field: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK && resp.StatusCode != 204 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Jira API error response: %s", string(body))
		return fmt.Errorf("Jira API request failed with status: %d", resp.StatusCode)
	}
	
	log.Printf("Successfully updated %s in %s (status: %d)", fieldID, jiraIssueKey, resp.StatusCode)
	return nil
}

// processComponentField processes a component custom field and updates the corresponding Jira field
func (s *IncidentJiraSync) processComponentField(customFieldEntry CustomFieldEntry, jiraIssueKey string, fieldMapping FieldMapping) error {
	var jiraValues []JiraComponentValue
	
	for _, value := range customFieldEntry.Values {
		if value.ValueCatalogEntry == nil {
			continue
		}
		
		catalogEntry := value.ValueCatalogEntry
		if catalogEntry.ID == "" {
			continue
		}
		
		// Get the object key from the catalog entry
		objectKey, err := s.getCatalogEntryObjectKey(catalogEntry.ID)
		if err != nil {
			log.Printf("Failed to get object key for catalog entry %s: %v", catalogEntry.ID, err)
			continue
		}
		
		// Extract the numeric ID
		objectID, err := s.extractJiraObjectID(objectKey)
		if err != nil {
			log.Printf("Failed to extract object ID from key %s: %v", objectKey, err)
			continue
		}
		
		// Format for Jira
		jiraValue := s.formatJiraComponentValue(objectID, catalogEntry.ID)
		jiraValues = append(jiraValues, jiraValue)
		
		log.Printf("Mapped %s -> %+v", catalogEntry.Name, jiraValue)
	}
	
	// Update Jira field
	if len(jiraValues) > 0 {
		err := s.updateJiraCustomField(jiraIssueKey, fieldMapping.JiraFieldID, jiraValues)
		
		// If Jira rejects multiple values, try with just the first one
		if err != nil && len(jiraValues) > 1 {
			log.Printf("Multiple values failed, trying with single value: %+v", jiraValues[0])
			return s.updateJiraCustomField(jiraIssueKey, fieldMapping.JiraFieldID, []JiraComponentValue{jiraValues[0]})
		}
		
		return err
	}
	
	return nil
}

// processIncidentUpdate processes incident update and syncs component fields to Jira
func (s *IncidentJiraSync) processIncidentUpdate(incidentData IncidentData) error {
	// Extract the incident data based on event type
	var incident struct {
		ID                       string                 `json:"id"`
		Name                     string                 `json:"name"`
		ExternalIssueReference   ExternalIssueReference `json:"external_issue_reference"`
		CustomFieldEntries       []CustomFieldEntry     `json:"custom_field_entries"`
	}
	
	if incidentData.EventType == "public_incident.incident_updated_v2" {
		incident = incidentData.PublicIncidentUpdatedV2
	} else {
		incident = incidentData.Incident
	}
	
	// Get Jira issue key
	jiraIssueKey := incident.ExternalIssueReference.IssueName
	if jiraIssueKey == "" {
		return fmt.Errorf("no Jira issue found for incident")
	}
	
	log.Printf("Processing incident update for Jira issue: %s", jiraIssueKey)
	
	// Process custom fields
	fieldMappings := s.getFieldMappings()
	for _, fieldEntry := range incident.CustomFieldEntries {
		fieldName := fieldEntry.CustomField.Name
		
		// Check if this is an impacted components field
		if fieldName == fieldMappings["impacted_components"].IncidentFieldName {
			log.Printf("Processing impacted components field")
			if err := s.processComponentField(fieldEntry, jiraIssueKey, fieldMappings["impacted_components"]); err != nil {
				log.Printf("Failed to process impacted components: %v", err)
				return err
			}
		}
		
		// Check if this is a responsible components field
		if fieldName == fieldMappings["responsible_components"].IncidentFieldName {
			log.Printf("Processing responsible components field")
			if err := s.processComponentField(fieldEntry, jiraIssueKey, fieldMappings["responsible_components"]); err != nil {
				log.Printf("Failed to process responsible components: %v", err)
				return err
			}
		}
	}
	
	return nil
}

// HTTP handlers
func (s *IncidentJiraSync) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	// TODO: Implement webhook secret verification if needed
	// if s.config.WebhookSecret != "" {
	// 	signature := r.Header.Get("X-Incident-Signature")
	// 	if signature == "" {
	// 		log.Printf("Missing webhook signature")
	// 		http.Error(w, "Missing signature", http.StatusUnauthorized)
	// 		return
	// 	}
	// 	// Verify signature against webhook secret
	// }
	
	// Parse webhook payload
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Failed to read request body: %v", err)
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	
	// Log webhook receipt for monitoring
	log.Printf("Webhook received from %s", r.RemoteAddr)
	
	var payload IncidentData
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("Failed to decode JSON payload: %v", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	
	// Log event details for monitoring
	log.Printf("Processing event type: %s", payload.EventType)
	
	// Only process incident update events
	if payload.EventType != "incident.custom_field_updated" && payload.EventType != "public_incident.incident_updated_v2" {
		log.Printf("Ignoring event type: %s", payload.EventType)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ignored"})
		return
	}
	
	// Process the incident update
	if err := s.processIncidentUpdate(payload); err != nil {
		log.Printf("Failed to process incident update: %v", err)
		http.Error(w, "Processing failed", http.StatusInternalServerError)
		return
	}
	
	log.Printf("Successfully processed incident update")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (s *IncidentJiraSync) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
}


func getConfig() Config {
	return Config{
		JiraBaseURL:                     getEnv("JIRA_BASE_URL", ""),
		JiraUsername:                    getEnv("JIRA_USERNAME", ""),
		JiraAPIToken:                    getEnv("JIRA_API_TOKEN", ""),
		IncidentAPIToken:                getEnv("INCIDENT_API_TOKEN", ""),
		WebhookSecret:                   getEnv("WEBHOOK_SECRET", ""),
		Port:                           getEnv("PORT", "5000"),
		JiraWorkspaceID:                getEnv("JIRA_WORKSPACE_ID", ""),
		ImpactedComponentFieldName:     getEnv("IMPACTED_COMPONENT_FIELD_NAME", "Impacted component"),
		ImpactedComponentJiraFieldID:   getEnv("IMPACTED_COMPONENT_JIRA_FIELD_ID", ""),
		ResponsibleComponentFieldName:  getEnv("RESPONSIBLE_COMPONENT_FIELD_NAME", "Responsible components"),
		ResponsibleComponentJiraFieldID: getEnv("RESPONSIBLE_COMPONENT_JIRA_FIELD_ID", ""),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	config := getConfig()
	
	// Validate configuration
	if config.JiraAPIToken == "" {
		log.Fatal("JIRA_API_TOKEN environment variable is required")
	}
	
	if config.IncidentAPIToken == "" {
		log.Fatal("INCIDENT_API_TOKEN environment variable is required")
	}
	
	if config.JiraBaseURL == "" {
		log.Fatal("JIRA_BASE_URL environment variable is required")
	}
	
	if config.JiraUsername == "" {
		log.Fatal("JIRA_USERNAME environment variable is required")
	}
	
	if config.JiraWorkspaceID == "" {
		log.Fatal("JIRA_WORKSPACE_ID environment variable is required")
	}
	
	if config.ImpactedComponentJiraFieldID == "" {
		log.Fatal("IMPACTED_COMPONENT_JIRA_FIELD_ID environment variable is required")
	}
	
	if config.ResponsibleComponentJiraFieldID == "" {
		log.Fatal("RESPONSIBLE_COMPONENT_JIRA_FIELD_ID environment variable is required")
	}
	
	// Initialize sync handler
	syncHandler := NewIncidentJiraSync(config)
	
	// Setup HTTP routes
	http.HandleFunc("/webhook", syncHandler.webhookHandler)
	http.HandleFunc("/health", syncHandler.healthHandler)
	
	log.Printf("Starting incident.io to Jira webhook listener on port %s...", config.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", config.Port), nil))
}