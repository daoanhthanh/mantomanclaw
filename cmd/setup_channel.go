package cmd

import (
	"fmt"
	"os"
)

// setupChannelStep optionally guides the user through channel setup.
func setupChannelStep() {
	fmt.Println("── Step 3: Channel (optional) ──")
	fmt.Println()

	setup, err := promptConfirm("Set up a messaging channel?", false)
	if err != nil || !setup {
		fmt.Println("  Skipped.")
		fmt.Println()
		return
	}

	typeOptions := []SelectOption[string]{
		{"Telegram", "telegram"},
		{"Discord", "discord"},
		{"Slack", "slack"},
		{"Mattermost", "mattermost"},
		{"Microsoft Teams", "msteams"},
	}
	channelType, err := promptSelect("Channel type", typeOptions, 0)
	if err != nil {
		return
	}

	name, err := promptString("Instance name", "", channelType+"-bot")
	if err != nil {
		return
	}

	// Credentials per type
	creds := map[string]string{}
	instanceCfg := map[string]any{}
	switch channelType {
	case "telegram":
		token, err := promptPassword("Bot token", "from @BotFather")
		if err != nil || token == "" {
			return
		}
		creds["token"] = token
	case "discord":
		token, err := promptPassword("Bot token", "from Discord Developer Portal")
		if err != nil || token == "" {
			return
		}
		creds["token"] = token
	case "slack":
		token, err := promptPassword("Bot token", "xoxb-...")
		if err != nil || token == "" {
			return
		}
		creds["token"] = token
		secret, err := promptPassword("Signing secret", "")
		if err != nil || secret == "" {
			return
		}
		creds["signing_secret"] = secret
	case "mattermost":
		serverURL, err := promptString("Server URL", "e.g. https://chat.example.com", "")
		if err != nil || serverURL == "" {
			return
		}
		instanceCfg["server_url"] = serverURL
		token, err := promptPassword("Bot token", "from Integrations → Bot Accounts")
		if err != nil || token == "" {
			return
		}
		creds["bot_token"] = token
	case "msteams":
		appID, err := promptString("App ID (Azure AD)", "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", "")
		if err != nil || appID == "" {
			return
		}
		creds["app_id"] = appID
		tenantID, err := promptString("Tenant ID (Azure AD)", "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx", "")
		if err != nil || tenantID == "" {
			return
		}
		creds["tenant_id"] = tenantID
		fmt.Println("Authentication: enter a client secret OR paths to certificate/key PEM files.")
		clientSecret, err := promptPassword("Client secret (leave empty to use certificate)", "")
		if err != nil {
			return
		}
		if clientSecret != "" {
			creds["client_secret"] = clientSecret
		} else {
			certPath, err := promptString("Certificate PEM path", "e.g. /etc/ssl/bot.crt", "")
			if err != nil {
				return
			}
			if certPath != "" {
				certData, err := os.ReadFile(certPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error reading certificate: %v\n", err)
					return
				}
				creds["certificate_pem"] = string(certData)
			}
			keyPath, err := promptString("Private key PEM path", "e.g. /etc/ssl/bot.key", "")
			if err != nil {
				return
			}
			if keyPath != "" {
				keyData, err := os.ReadFile(keyPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error reading private key: %v\n", err)
					return
				}
				creds["private_key_pem"] = string(keyData)
			}
		}
	}

	// Bind to agent
	agents, err := fetchAgentList()
	if err != nil || len(agents) == 0 {
		fmt.Fprintf(os.Stderr, "  No agents found. Create an agent first.\n")
		return
	}

	agentOptions := make([]SelectOption[string], len(agents))
	for i, a := range agents {
		agentOptions[i] = SelectOption[string]{
			Label: fmt.Sprintf("%s (%s)", a.AgentKey, a.DisplayName),
			Value: a.ID,
		}
	}
	agentID, err := promptSelect("Bind to agent", agentOptions, 0)
	if err != nil {
		return
	}

	body := map[string]any{
		"name":         name,
		"channel_type": channelType,
		"agent_id":     agentID,
		"enabled":      true,
		"credentials":  creds,
	}
	if len(instanceCfg) > 0 {
		body["config"] = instanceCfg
	}

	_, err = gatewayHTTPPost("/v1/channels/instances", body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
		return
	}

	fmt.Printf("  Channel %q (%s) created.\n\n", name, channelType)
	fmt.Println("  Note: For Zalo, Feishu, WhatsApp — use the Web Dashboard.")
	fmt.Println()
}
