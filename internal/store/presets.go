package store

// SitePresets are the category presets shown on the Lists page.
var SitePresets = map[string][]string{
	"Video":     {"youtube.com", "youtu.be", "ytimg.com", "googlevideo.com", "youtube-nocookie.com", "netflix.com", "nflxvideo.net", "nflximg.net", "nflxext.com", "nflxso.net", "hulu.com", "disneyplus.com", "max.com", "primevideo.com"},
	"Social":    {"tiktok.com", "tiktokcdn.com", "tiktokv.com", "instagram.com", "cdninstagram.com", "snapchat.com", "sc-cdn.net", "reddit.com", "redd.it", "redditstatic.com", "redditmedia.com", "twitter.com", "x.com", "twimg.com", "facebook.com", "threads.net", "tumblr.com", "pinterest.com"},
	"Streaming": {"twitch.tv", "ttvnw.net", "jtvnw.net", "kick.com"},
	"Chat":      {"discord.com", "discord.gg", "discordapp.com", "discordapp.net", "discord.media"},
	"Gaming":    {"steampowered.com", "steamcommunity.com", "steamstatic.com", "epicgames.com", "roblox.com", "rbxcdn.com", "minecraft.net", "miniclip.com", "poki.com", "crazygames.com", "y8.com", "kongregate.com", "itch.io", "fortnite.com", "ea.com", "battle.net", "blizzard.com", "riotgames.com", "leagueoflegends.com", "xbox.com", "playstation.com"},
}

// DefaultCategories are on by default; Shopping-style extras are left to the parent.
var DefaultCategories = []string{"Video", "Social", "Streaming", "Chat", "Gaming"}

// CategoryOrder keeps the UI stable.
var CategoryOrder = []string{"Video", "Social", "Streaming", "Chat", "Gaming"}
