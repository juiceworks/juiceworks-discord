package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

const (
	JuiceworksGuildId    = "1256628364987600977"
	InternalChannelId    = "1256628365771669556"
	JuiceworksRoleId     = "1257752490372370503"
	ProjectCreatorRoleId = "1259262543034060830"
	ServicesRoleId       = "1260738526425780264"
)

var commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
	"make-channel": makeChannel,
	"add-member":   addMember,
}

func main() {
	// Load the Discord token from the environment or .env file
	var discordToken string
	if t := os.Getenv("DISCORD_TOKEN"); t != "" {
		discordToken = t
	} else {
		if _, err := os.Stat(".env"); err != nil {
			log.Fatalf("Could not load .env file: %s\n", err)
		}
		godotenv.Load(".env")
		discordToken = os.Getenv("DISCORD_TOKEN")
		if discordToken == "" {
			log.Fatalln("Could not find DISCORD_TOKEN in .env file or environment.")
		}
	}

	// Create the Discord session.
	s, err := discordgo.New("Bot " + discordToken)
	if err != nil {
		log.Fatalf("Could not create Discord session: %s\n", err)
	}

	// Configure session & log on ready.
	s.ShouldReconnectOnError = true
	s.ShouldRetryOnRateLimit = true
	s.LogLevel = discordgo.LogError
	s.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Logged in as: %s\n", s.State.User)
	})

	// Call the appropriate command handler when an interaction is created.
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})

	// Open the Discord session.
	if err = s.Open(); err != nil {
		log.Fatalf("Could not open Discord session: %s\n", err)
	}
	defer s.Close()

	// Register slash commands in the Juiceworks guild.
	registeredCommands := make([]*discordgo.ApplicationCommand, len(commands))
	for i, v := range commands {
		cmd, err := s.ApplicationCommandCreate(s.State.User.ID, JuiceworksGuildId, v)
		if err != nil {
			log.Panicf("Cannot create '%v' command: %v", v.Name, err)
		}
		registeredCommands[i] = cmd
	}

	// Clean up the commands when the program exits.
	defer func() {
		for _, v := range registeredCommands {
			err := s.ApplicationCommandDelete(s.State.User.ID, JuiceworksGuildId, v.ID)
			if err != nil {
				log.Panicf("Cannot delete '%v' command: %v", v.Name, err)
			}
		}
	}()

	// Wait for a signal to shutdown.
	log.Println("Bot is running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
	log.Println("Shutting down...")
}

// Add a user to a private channel, and grant them the Project Creator role.
func addMember(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := checkCommandCaller(s, i); err != nil {
		log.Printf("Command caller check failed on addMember: %v", err)
		return
	}

	// Prevent adding new members to the internal channel.
	if i.ChannelID == InternalChannelId {
		logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "This command cannot be used in the internal channel.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		return
	}

	// Verify the command options.
	options := i.ApplicationCommandData().Options
	if len(options) == 0 || options[0].Type != discordgo.ApplicationCommandOptionUser {
		logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "This command requires a user.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		return
	}
	user := options[0].UserValue(s)

	// Get the user's roles
	member, err := s.GuildMember(JuiceworksGuildId, user.ID)
	if err != nil {
		log.Printf("Error reading member roles: %v", err)
		logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Error reading member roles: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		return
	}

	// Check if the user is a service provider
	isServiceProvider := false
	for _, roleID := range member.Roles {
		if roleID == ServicesRoleId {
			isServiceProvider = true
			break
		}
	}

	// If the user isn't a service provider, grant them the Project Creator role.
	if !isServiceProvider {
		if err := s.GuildMemberRoleAdd(JuiceworksGuildId, user.ID, ProjectCreatorRoleId); err != nil {
			log.Printf("Error granting Project Creator role: %v", err)
			logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: "Error granting Project Creator role: " + err.Error(),
					Flags:   discordgo.MessageFlagsEphemeral,
				},
			}))
			return
		}
	}

	// Add the user to the channel the command was called from.
	if err := channelPermissions(&channelPermissionSetup{
		s:           s,
		channelID:   i.ChannelID,
		targetID:    user.ID,
		targetType:  discordgo.PermissionOverwriteTypeMember,
		allow:       discordgo.PermissionViewChannel | discordgo.PermissionSendMessages,
		deny:        0,
		interaction: i,
	}); err != nil {
		log.Printf("Error adding member to channel: %v", err)
		logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Error adding member to channel: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		return
	}

	// Respond to the interaction.
	log.Printf("Added %s (%s) to channel %s.", user, user.Mention(), i.ChannelID)
	logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Added %s to the channel.", user.Mention()),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}))
}

// Make a private channel for a new project. Add the project creator and Juiceworks members to the channel.
func makeChannel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := checkCommandCaller(s, i); err != nil {
		log.Printf("Command caller check failed on makeChannel: %v", err)
		return
	}

	// Verify the command options.
	options := i.ApplicationCommandData().Options
	if len(options) == 0 || options[0].Type != discordgo.ApplicationCommandOptionString {
		logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "This command requires a channel name.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		return
	}

	// Clean up the channel name.
	channelName := options[0].StringValue()
	channelName = strings.TrimSpace(channelName)
	channelName = strings.ToLower(channelName)
	channelName = strings.ReplaceAll(channelName, " ", "-")
	if len(channelName) < 2 || len(channelName) > 100 {
		logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Channel name must be between 2 and 100 characters.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		return
	}

	// Create the channel.
	channel, err := s.GuildChannelCreate(JuiceworksGuildId, channelName, discordgo.ChannelTypeGuildText)
	if err != nil {
		logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Error creating channel: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		return
	}

	// Set the permissions for the channel.
	permissionsToSet := []channelPermissionSetup{
		// Add the Juiceworks role to the channel.
		{s, channel.ID, JuiceworksRoleId, discordgo.PermissionOverwriteTypeRole, discordgo.PermissionViewChannel | discordgo.PermissionSendMessages, 0, i},
		// Make the channel private.
		{s, channel.ID, JuiceworksGuildId, discordgo.PermissionOverwriteTypeRole, 0, discordgo.PermissionViewChannel, i},
	}
	for _, p := range permissionsToSet {
		if err := channelPermissions(&p); err != nil {
			return
		}
	}

	// Respond to the interaction.
	log.Printf("Created channel: %v", channel)
	logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Created channel: #" + channel.Name,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}))
}

// A struct to hold the parameters for setting channel permissions.
type channelPermissionSetup struct {
	s           *discordgo.Session
	channelID   string
	targetID    string
	targetType  discordgo.PermissionOverwriteType
	allow       int64
	deny        int64
	interaction *discordgo.InteractionCreate
}

// Set the permissions for a channel. If it fails, respond to the interaction and log/return the error.
func channelPermissions(cps *channelPermissionSetup) error {
	err := cps.s.ChannelPermissionSet(cps.channelID, cps.targetID, cps.targetType, cps.allow, cps.deny)
	if err != nil {
		logResponseErr(cps.s.InteractionRespond(cps.interaction.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Error setting channel permissions: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		log.Printf("Error setting channel permissions: %v", err)
		return err
	}

	return nil
}

// Wrapper to log an error if responding to an interaction fails.
func logResponseErr(err error) {
	if err != nil {
		log.Printf("Error responding to interaction: %v", err)
	}
}

// Make sure a command is being called in the Juiceworks Discord server, by a Juiceworks member.
func checkCommandCaller(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	// Check if the command was called in the Juiceworks Discord server.
	if i.GuildID != JuiceworksGuildId || i.Member == nil {
		logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "This command can only be used in the Juiceworks Discord server.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		return fmt.Errorf("command was called outside of the Juiceworks Discord server")
	}

	// Check if the caller has the Juiceworks role.
	callerHasJuiceworksRole := false
	for _, role := range i.Member.Roles {
		if role == JuiceworksRoleId {
			callerHasJuiceworksRole = true
			break
		}
	}
	if !callerHasJuiceworksRole {
		logResponseErr(s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "This command can only be used by Juiceworks members.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}))
		return fmt.Errorf("command was called by a non-Juiceworks member")
	}

	return nil
}

// The slash commands to register in the Juiceworks guild.
var commands = []*discordgo.ApplicationCommand{
	{
		Type:        discordgo.ChatApplicationCommand,
		Name:        "make-channel",
		Description: "Create a channel for a new project.",
		GuildID:     JuiceworksGuildId,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "channel-name",
				Description: "What to name the channel",
				Required:    true,
			},
		},
	},
	{
		Type:        discordgo.ChatApplicationCommand,
		Name:        "add-member",
		Description: "Add a member to this channel. Use in a channel to add someone.",
		GuildID:     JuiceworksGuildId,
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "The user to add to the channel",
				Required:    true,
			},
		},
	},
}
