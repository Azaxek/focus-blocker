package apps

// Presets are the built-in rules; the first six form the default global list.
var Presets = []Rule{
	{ID: "steam", Name: "Steam", Exe: []string{"steam.exe", "steamwebhelper.exe"}, Original: []string{"steam.exe"}, Path: []string{`\steam\steamapps\`, `\steam\bin\`}, Publisher: []string{"Valve"}},
	{ID: "discord", Name: "Discord", Exe: []string{"discord.exe", "discordptb.exe", "discordcanary.exe"}, Original: []string{"discord.exe"}, Path: []string{`\appdata\local\discord\`}, Publisher: []string{"Discord Inc"}},
	{ID: "epic", Name: "Epic Games", Exe: []string{"epicgameslauncher.exe", "epicwebhelper.exe", "fortniteclient-win64-shipping.exe"}, Path: []string{`\epic games\`}, Publisher: []string{"Epic Games"}},
	{ID: "minecraft", Name: "Minecraft", Exe: []string{"minecraft.exe", "minecraftlauncher.exe", "minecraft.windows.exe"}, Path: []string{`\.minecraft\`, `\minecraft launcher\`}, Package: []string{"Microsoft.4297127D64EC6", "Microsoft.MinecraftUWP"}},
	{ID: "roblox", Name: "Roblox", Exe: []string{"robloxplayerbeta.exe", "robloxplayerlauncher.exe", "robloxcrashhandler.exe"}, Path: []string{`\roblox\versions\`}, Publisher: []string{"Roblox Corporation"}},
	{ID: "spotify", Name: "Spotify", Exe: []string{"spotify.exe"}, Original: []string{"spotify.exe"}, Package: []string{"SpotifyAB.SpotifyMusic"}, Publisher: []string{"Spotify AB"}},
	{ID: "battlenet", Name: "Battle.net", Exe: []string{"battle.net.exe", "battle.net launcher.exe"}, Path: []string{`\battle.net\`}, Publisher: []string{"Blizzard Entertainment"}},
	{ID: "ea", Name: "EA app / Origin", Exe: []string{"eadesktop.exe", "origin.exe", "eabackgroundservice.exe"}, Path: []string{`\ea app\`, `\origin games\`}, Publisher: []string{"Electronic Arts"}},
	{ID: "riot", Name: "Riot Games", Exe: []string{"riotclientservices.exe", "leagueclient.exe", "valorant-win64-shipping.exe"}, Path: []string{`\riot games\`}, Publisher: []string{"Riot Games"}},
	{ID: "ubisoft", Name: "Ubisoft Connect", Exe: []string{"ubisoftconnect.exe", "upc.exe"}, Path: []string{`\ubisoft game launcher\`}, Publisher: []string{"Ubisoft"}},
	{ID: "gog", Name: "GOG Galaxy", Exe: []string{"galaxyclient.exe"}, Path: []string{`\gog galaxy\`}, Publisher: []string{"GOG sp"}},
	{ID: "twitch", Name: "Twitch", Exe: []string{"twitch.exe"}, Path: []string{`\twitch\`}, Publisher: []string{"Twitch Interactive"}},
	{ID: "tiktok", Name: "TikTok", Exe: []string{"tiktok.exe"}, Path: []string{`\tiktok\`}},
	{ID: "netflix", Name: "Netflix", Package: []string{"4DF9E0F8.Netflix"}},
}

// DefaultGlobal is the starting global app list.
var DefaultGlobal = []string{"steam", "discord", "epic", "minecraft", "roblox", "spotify"}
