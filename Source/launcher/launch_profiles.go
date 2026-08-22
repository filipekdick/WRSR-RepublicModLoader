package main

type LaunchProfile string

const (
	profileNormal   LaunchProfile = "normal"
	profileDeepTest LaunchProfile = "deep-test"
)

type LaunchProfileInfo struct {
	ID          LaunchProfile
	Name        string
	Short       string
	Description string
	DeepTest    bool
}

var launchProfiles = []LaunchProfileInfo{
	{
		ID: profileNormal, Name: "Normal / recommended", Short: "NORMAL",
		Description: "Reliable RML launch with concise session logging.",
	},
	{
		ID: profileDeepTest, Name: "Detailed diagnostic launch", Short: "DIAGNOSTIC",
		Description: "Loads the same enabled plugins while collecting exhaustive lifecycle, hook and memory-change evidence until WRSR closes.",
		DeepTest:    true,
	},
}

func normalizeLaunchProfile(value string) LaunchProfile {
	for _, p := range launchProfiles {
		if string(p.ID) == value {
			return p.ID
		}
	}
	return profileNormal
}

func launchProfileByID(id LaunchProfile) LaunchProfileInfo {
	id = normalizeLaunchProfile(string(id))
	for _, p := range launchProfiles {
		if p.ID == id {
			return p
		}
	}
	return launchProfiles[0]
}
