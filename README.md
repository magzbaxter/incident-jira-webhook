# incident.io to Jira Component Sync Webhook

This webhook automatically synchronizes component custom fields from incident.io to Jira, eliminating the need for manual updates and ensuring consistency between systems.

## 🚀 Quick Start with Docker

### Prerequisites
- Docker installed on your system
- incident.io API token with read access to catalog entries
- Jira API token with permission to update custom fields
- Your Jira custom field IDs for components

### 1. Build the Docker Image

```bash
# Clone or download the project files
# Navigate to the project directory

# Build the Docker image
docker build -t incident-jira-webhook .
```

### 2. Run with Docker

```bash
docker run -d \
  --name incident-jira-webhook \
  -p 5000:5000 \
  -e JIRA_BASE_URL="https://your-domain.atlassian.net" \
  -e JIRA_USERNAME="your-email@company.com" \
  -e JIRA_API_TOKEN="your-jira-api-token" \
  -e INCIDENT_API_TOKEN="your-incident-api-token" \
  -e WEBHOOK_SECRET="optional-webhook-secret" \
  --restart unless-stopped \
  incident-jira-webhook
```

### 3. Using Docker Compose (Recommended)

Create a `docker-compose.yml` file:

```yaml
version: '3.8'
services:
  incident-jira-webhook:
    build: .
    ports:
      - "5000:5000"
    environment:
      - JIRA_BASE_URL=https://your-domain.atlassian.net
      - JIRA_USERNAME=your-email@company.com
      - JIRA_API_TOKEN=your-jira-api-token
      - INCIDENT_API_TOKEN=your-incident-api-token
      - WEBHOOK_SECRET=optional-webhook-secret
      - PORT=5000
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://localhost:5000/health"]
      interval: 30s
      timeout: 10s
      retries: 3
```

Then run:
```bash
docker-compose up -d
```

## 📋 Configuration

### Required Environment Variables

| Variable | Description |
|----------|-------------|
| `JIRA_BASE_URL` | Your Jira instance URL |
| `JIRA_USERNAME` | Your Jira username/email |
| `JIRA_API_TOKEN` | Jira API token |
| `JIRA_WORKSPACE_ID` | Your Jira workspace ID for components |
| `INCIDENT_API_TOKEN` | incident.io API token |
| `IMPACTED_COMPONENT_JIRA_FIELD_ID` | Jira field ID for impacted components |
| `RESPONSIBLE_COMPONENT_JIRA_FIELD_ID` | Jira field ID for responsible components |

### Optional Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `IMPACTED_COMPONENT_FIELD_NAME` | `Impacted component` | incident.io field name |
| `RESPONSIBLE_COMPONENT_FIELD_NAME` | `Responsible components` | incident.io field name |
| `WEBHOOK_SECRET` | - | Optional webhook verification secret |
| `PORT` | `5000` | Port to run the webhook listener on |

## 🔧 Production Deployment

### With Reverse Proxy (Recommended)

Use nginx or traefik to handle HTTPS:

```yaml
# docker-compose.yml
version: '3.8'
services:
  incident-jira-webhook:
    build: .
    environment:
      - JIRA_BASE_URL=https://your-domain.atlassian.net
      - JIRA_USERNAME=your-email@company.com
      - JIRA_API_TOKEN=your-jira-api-token
      - INCIDENT_API_TOKEN=your-incident-api-token
    restart: unless-stopped
    networks:
      - webhook-network

  nginx:
    image: nginx:alpine
    ports:
      - "443:443"
      - "80:80"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf
      - ./ssl:/etc/nginx/ssl
    depends_on:
      - incident-jira-webhook
    networks:
      - webhook-network

networks:
  webhook-network:
    driver: bridge
```

### Environment File

Create a `.env` file for sensitive data:

```bash
# .env
JIRA_BASE_URL=https://your-domain.atlassian.net
JIRA_USERNAME=your-email@company.com
JIRA_API_TOKEN=your-jira-api-token
INCIDENT_API_TOKEN=your-incident-api-token
WEBHOOK_SECRET=your-webhook-secret
```

Update docker-compose.yml:
```yaml
env_file:
  - .env
```

**⚠️ Important**: Add `.env` to your `.gitignore` file!

## 🔗 Webhook Setup

1. **Get your webhook URL**: `https://your-domain.com/webhook`
2. **Configure in incident.io**:
   - Go to Settings → Webhooks
   - Add new webhook
   - URL: Your webhook endpoint
   - Events: Select `incident.custom_field_updated`
   - Secret: Use your `WEBHOOK_SECRET` if configured

## 🧪 Testing

### Health Check
```bash
curl http://localhost:5000/health
# Should return: {"status":"healthy"}
```

### Test Webhook Locally
```bash
# Send test webhook payload
curl -X POST http://localhost:5000/webhook \
  -H "Content-Type: application/json" \
  -d @test-payload.json
```

### View Logs
```bash
# Docker logs
docker logs incident-jira-webhook

# Follow logs
docker logs -f incident-jira-webhook

# Docker Compose logs
docker-compose logs -f incident-jira-webhook
```

## 🔍 Troubleshooting

### Common Issues

1. **"No object key found"**
   - Ensure your catalog entries have an "object key" attribute
   - Check the attribute name matches exactly (case sensitive)

2. **Jira API errors**
   - Verify your API token has correct permissions
   - Check if custom field IDs are correct
   - Ensure the field accepts the component format

3. **Webhook not triggering**
   - Verify the webhook URL is accessible from internet
   - Check if HTTPS is properly configured
   - Ensure the webhook is configured for the right event type

### Debug Mode

Add debug logging by setting:
```bash
-e LOG_LEVEL=debug
```

## 📊 Monitoring

The webhook includes a health endpoint for monitoring:
- **Endpoint**: `GET /health`
- **Response**: `{"status":"healthy"}`
- **HTTP Status**: 200 OK

Use this with your monitoring system (Prometheus, Datadog, etc.).

## 🔒 Security Best Practices

1. **Use HTTPS**: Always deploy with HTTPS in production
2. **Firewall**: Only expose necessary ports
3. **API Tokens**: Store securely, rotate regularly
4. **Webhook Secret**: Use webhook secrets for verification
5. **Network**: Consider running in a private network/VPN

## 🛠️ How It Works

1. **Webhook Received**: incident.io sends `incident.custom_field_updated` event
2. **Field Check**: Verifies if updated field is a component field
3. **Catalog Lookup**: Fetches component details from incident.io catalog API
4. **Object Key Extraction**: Gets the "object key" attribute (e.g., "PIN-3")
5. **ID Parsing**: Extracts numeric ID from object key (e.g., "3")
6. **Jira Update**: Updates corresponding Jira custom field with proper format:
   ```json
   {
     "id": "catalog-entry-id:3",
     "objectId": "3"
   }
   ```

## 📄 Example Workflow

1. **incident.io**: User sets "Teletraan" as impacted component
2. **Webhook**: incident.io sends webhook to your service
3. **Lookup**: Service fetches "Teletraan" catalog entry
4. **Extract**: Finds object key "PIN-3" in catalog attributes
5. **Parse**: Extracts "3" from "PIN-3"
6. **Update**: Updates Jira `customfield_10234` with component ID "3"

## 📞 Support

For issues with this webhook service:
1. Check the logs for error messages
2. Verify your configuration matches your Jira setup
3. Test API connectivity manually
4. Ensure webhook payload matches expected format

## 📁 Project Structure

```
incident-jira-webhook/
├── incident-jira-webhook.go    # Main application
├── go.mod                      # Go module definition
├── Dockerfile                  # Docker build instructions
├── docker-compose.yml          # Docker Compose configuration
├── .env.example               # Environment variables template
├── .gitignore                 # Git ignore file
└── README.md                  # This documentation
```

## 🚢 Deployment Instructions

1. **Create project directory:**
   ```bash
   mkdir incident-jira-webhook
   cd incident-jira-webhook
   ```

2. **Copy all project files into this directory**

3. **Set up environment:**
   ```bash
   cp .env.example .env
   # Edit .env with your actual API tokens and URLs
   ```

4. **Deploy with Docker Compose:**
   ```bash
   docker-compose up -d
   ```

5. **Configure incident.io webhook** to point to your deployed service

That's it! Your webhook listener will now automatically sync component fields from incident.io to Jira.