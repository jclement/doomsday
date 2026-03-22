// Package whimsy provides apocalyptic/paranoid messages, taglines, and greetings
// for the doomsday backup tool. Messages are randomly selected on each execution.
//
// Rules from the spec:
//   - Never in error output.
//   - Reduced in cron mode.
//   - Disabled with whimsy=false or --no-whimsy.
//   - Apocalyptic/paranoid tone. "Your data survived another day" fits.
//     "Your bits are tucked in safe and sound" does not.
package whimsy

import (
	"math/rand"
	"sync"
	"time"
)

var (
	mu      sync.RWMutex
	enabled = true
	rng     *rand.Rand
)

func init() {
	seedRNG()
}

// seedRNG creates a per-execution RNG so messages are fresh every time.
func seedRNG() {
	rng = rand.New(rand.NewSource(time.Now().UnixNano()))
}

// SetEnabled controls whether whimsy messages are returned.
// When disabled, all message functions return empty strings.
func SetEnabled(on bool) {
	mu.Lock()
	defer mu.Unlock()
	enabled = on
}

// IsEnabled returns whether whimsy is currently enabled.
func IsEnabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return enabled
}

func pick(pool []string) string {
	mu.Lock()
	defer mu.Unlock()
	if !enabled {
		return ""
	}
	return pool[rng.Intn(len(pool))]
}

// --- Message Pools ---

var greetings = []string{
	"Your data survived another day.",
	"The apocalypse can wait. Your backups cannot.",
	"Trust no one. Back up everything.",
	"Another day, another backup. The end times are patient.",
	"The vaults are sealed. The data endures.",
	"Paranoia is just good planning with better marketing.",
	"Your bits are entombed in layers of encryption. As they should be.",
	"The world may end, but your files will outlast it.",
	"Somewhere, a hard drive is dying. Not yours. Not today.",
	"The bunker is stocked. The checksums are valid.",
	"All data is ephemeral. Yours just less so.",
	"Every byte accounted for. Every sector verified.",
	"The electromagnetic pulse hasn't happened yet. You're welcome.",
	"Your files are safer than most governments' classified archives.",
	"Sleep soundly. Your data stands watch.",
	"Entropy comes for us all. Your backups buy you time.",
	"The only thing between you and total data loss is this software. No pressure.",
	"The dead hand system is online. Your data persists.",
	"Another sunrise. Another day your data didn't perish.",
	"Welcome back. The archive remembers everything.",
	"Your data bunker withstood another night. Status: unbreached.",
	"Digital immortality, one snapshot at a time.",
	"The servers hum. The checksums hold. All is as it should be.",
	"While you slept, your backups kept vigil.",
	"No cosmic rays corrupted your archives overnight. Probably.",
	"Bit rot is patient. So are we.",
	"Your data is more resilient than the civilization that created it.",
	"Every file present. Every hash correct. Another good day.",
	"The doomsday clock ticks. Your backups don't care.",
	"Somewhere a datacenter is flooding. Your data is elsewhere.",
	"Three copies, two media, one offsite. You know the drill.",
}

var backupStartMessages = []string{
	"Engaging backup protocols. Stand by for data preservation.",
	"Sealing another time capsule for the post-apocalypse.",
	"Chunking, encrypting, deduplicating. The holy trinity.",
	"Your data is about to become a lot more durable.",
	"Initiating paranoid data hoarding sequence.",
	"Scanning for changes. The filesystem confesses everything.",
	"Beginning the ritual of data preservation.",
	"Locking the vault doors. Backup in progress.",
	"Your future self will thank you for this moment.",
	"Cataloging reality before entropy gets any ideas.",
	"Firing up the deduplication engine. Redundancy is the enemy.",
	"Reading every byte like it might be the last time.",
	"Backing up like there's no tomorrow. Because there might not be.",
	"Engaging content-defined chunking. Each byte earns its place.",
	"The dead man's switch is armed. Backup commencing.",
	"Harvesting changed blocks from the filesystem.",
	"Spinning up the encryption pipeline. Trust no wire.",
	"Compressing your digital life into immutable packs.",
	"Another offering to the archive gods.",
	"Scanning, hashing, packing. The machine feeds.",
	"Converting filesystem anxiety into encrypted blobs.",
	"Your files are about to become cryptographic puzzles. Good luck, attackers.",
	"Engaging paranoid mode. Every bit will be verified.",
	"The vault demands its tribute. Commencing backup.",
	"Shredding your data into content-addressed chunks. Beautifully.",
	"Running the backup gauntlet. Scan, chunk, encrypt, store.",
	"Preserving today's state for tomorrow's archaeologists.",
	"Feeding the deduplication engine. It's always hungry.",
	"Another snapshot for the bunker. The archive grows.",
	"Inventorying your digital existence. Please stand by.",
	"Sealing the blast doors. Backup sequence initiated.",
}

var backupCompleteMessages = []string{
	"Another snapshot sealed in the vault.",
	"Data entombed. The future will thank you.",
	"Backup complete. One less thing to worry about when civilization falls.",
	"Your data is now more durable than most governments.",
	"Sealed, encrypted, deduplicated. Perfection.",
	"Snapshot committed. The timeline is preserved.",
	"All packs uploaded. All indexes written. All is well.",
	"The vault grows stronger. Another snapshot joins the archive.",
	"Backup successful. Your paranoia has been validated.",
	"Data secured. Entropy denied. For now.",
	"Every bit verified. Every chunk accounted for. Done.",
	"The dead man's switch has been reset. See you next time.",
	"Snapshot locked down. Try harder, bit rot.",
	"Another day, another snapshot. The archive deepens.",
	"Backup complete. The cockroaches will have something to read.",
	"All data committed to the vault. Nothing was lost.",
	"The archive accepts your offering. Integrity confirmed.",
	"Packs sealed. Indexes updated. Paranoia satisfied.",
	"Your data survived the pipeline. Encrypted and deduplicated.",
	"The vault door closes. Another snapshot preserved in amber.",
	"Backup verified. Your future self owes you one.",
	"All chunks accounted for. The deduplication ratio pleases us.",
	"Entropy pushed back another day. The archive holds.",
	"Snapshot committed to the bunker. The timeline is intact.",
	"Your bytes are sealed under layers of encryption. Rest easy.",
	"Backup locked down. Even we can't read it without the key.",
	"The archive deepens. Another layer of protection against the void.",
	"All systems nominal. Snapshot integrity: cryptographically guaranteed.",
	"Data preserved. When the dust settles, your files will remain.",
	"Another successful backup. The dead man's switch resets.",
	"Pack files sealed. The cockroaches envy your data retention policy.",
}

var idleStatusMessages = []string{
	"All systems nominal. Waiting for the next scheduled backup.",
	"Standing guard over your data. Nothing to report.",
	"Idle. But vigilant. Always vigilant.",
	"No backups due. Enjoying the calm before the next storm.",
	"Monitoring. Watching. Waiting. Your data is safe.",
	"All quiet on the backup front.",
	"Systems green. Data integrity confirmed. Standing by.",
	"Nothing to do but wait and verify checksums.",
	"The vault is sealed. The sentries are posted. All is quiet.",
	"Hibernating between backups. One eye open.",
	"All snapshots accounted for. All destinations reachable.",
	"Your backups are current. The world can end on schedule.",
	"Resting, but ready. The next backup is always around the corner.",
	"Checksums verified. Indexes intact. Status: operational.",
	"No threats detected. Data integrity holding steady.",
	"Quietly watching your filesystem for signs of change.",
	"The vault hums softly. All is well. For now.",
	"Standby mode. The backup engine sleeps with one eye open.",
	"No changes detected. The filesystem is suspiciously quiet.",
	"Waiting for entropy to make its move. We're ready.",
	"Perimeter secure. No data loss events detected.",
	"The archive is complete and current. Nothing to fear. Much.",
	"Cooling down after the last backup. Enjoying the silence.",
	"Between backups. The calm in the eye of the data storm.",
	"Sentinel mode active. Watching for filesystem changes.",
	"All destinations online. All snapshots verified. Standing by.",
	"The dead man's switch is armed. Awaiting next backup window.",
	"Status: fully backed up. Paranoia level: nominal.",
	"Nothing has changed since the last backup. Suspicious.",
	"Idle cycles. The backup engine contemplates the void.",
	"Your data fortress stands unbreached. Waiting for orders.",
}

var versionTaglines = []string{
	"Backup software for the beautifully paranoid.",
	"Because hard drives die and clouds evaporate.",
	"Your data's last line of defense.",
	"Encrypting your regrets since day one.",
	"The cockroach of backup software.",
	"Still here after the heat death of your last SSD.",
	"Built for the end of the world. Tested on Tuesdays.",
	"Trust nothing. Back up everything. Verify twice.",
	"Making sure your memes survive the apocalypse.",
	"For when RAID is just a suggestion.",
	"Because 'it was working yesterday' isn't a backup strategy.",
	"The last software standing after the great bit flip.",
	"Deduplicating entropy one chunk at a time.",
	"Paranoid by design. Resilient by necessity.",
	"Your data's insurance policy against the inevitable.",
	"When the drives fail, the backups remain.",
	"Outliving your hardware since day one.",
	"Digital preservation for the deeply suspicious.",
	"Your data's doomsday bunker.",
	"For those who trust math more than luck.",
	"Because 'the cloud' is just someone else's failing computer.",
	"Data loss is a choice. You chose differently.",
	"Surviving bit rot, one chunk at a time.",
	"The only backup tool that shares your paranoia.",
	"Built to outlast the infrastructure it runs on.",
	"Where your data goes to survive the end times.",
	"Encrypting today for tomorrow's archaeologists.",
	"Your files' witness protection program.",
	"Born from paranoia. Hardened by entropy.",
	"Making data loss statistically improbable since day one.",
	"For when 'move to trash' keeps you up at night.",
}

var restoreStartMessages = []string{
	"Reaching into the vault. Stand by for resurrection.",
	"Cracking open a snapshot. Time travel in progress.",
	"Decrypting the archive. The past is about to become the present.",
	"Reassembling your data from encrypted shards.",
	"Reversing the entropy. Your files are coming back.",
	"The vault opens. Data flows like ancient prophecy fulfilled.",
	"Pulling chunks from the abyss. Restore in progress.",
	"Decrypting, decompressing, reassembling. The unholy trinity.",
	"Time to undo the damage. The archive remembers.",
	"Opening the time capsule. Contents: your digital life.",
	"The backup gods smile upon you. Restore commencing.",
	"Unlocking the vault. Every byte accounted for.",
	"Resurrecting data from its encrypted tomb.",
	"The archive yields its secrets. Restore in progress.",
	"Packs cracked open. Chunks streaming. Data incoming.",
	"Reversing the backup pipeline. Destination: your filesystem.",
	"Extracting from the vault. Integrity verified on every chunk.",
	"The dead rise again. Well, your files do.",
	"Unraveling the encrypted archive, one pack at a time.",
	"Your past self preserved this moment. Let's see what they saved.",
	"The snapshot speaks. Decryption keys accepted.",
}

var restoreCompleteMessages = []string{
	"All files restored. The timeline has been corrected.",
	"Restoration complete. Your data has returned from the vault.",
	"Everything is back where it belongs. The archive delivers.",
	"Restore verified. Not a single bit out of place.",
	"The vault has spoken. All data restored successfully.",
	"Files resurrected. Integrity confirmed. Welcome back.",
	"Restore complete. The archive keeps its promises.",
	"Your data has emerged from encryption, intact and verified.",
	"All chunks reassembled. All hashes verified. Restoration complete.",
	"The past has been recovered. Every byte accounted for.",
	"Restore successful. The dead man's switch acknowledges.",
	"Data restored to its former glory. Not a byte was lost.",
	"The archive delivered. As it always does.",
	"All files verified against their original checksums. Perfect match.",
	"Restoration complete. Entropy has been reversed.",
	"Your data walked through fire and came back unscathed.",
	"Snapshot fully restored. The timeline is healed.",
	"Every chunk verified. Every file placed. Restore complete.",
	"The vault closes. Your data is home.",
	"Restored and verified. The archive never forgets.",
	"All packs decrypted. All files intact. Mission accomplished.",
}

var browsingMessages = []string{
	"Browsing the archive. Every file frozen in time.",
	"Peering into the vault. What treasures lie within?",
	"Navigating the encrypted depths of your backup.",
	"Exploring the snapshot. A moment preserved in amber.",
	"The archive opens its pages. Browse at will.",
	"Diving into the tree. Every directory a time capsule.",
	"Wandering the halls of your encrypted archive.",
	"The filesystem as it was. Frozen, encrypted, preserved.",
	"Examining the snapshot's contents. History laid bare.",
	"Your files, exactly as they were. The archive doesn't lie.",
	"Exploring the vault. Each file a frozen memory.",
	"The snapshot reveals its secrets. Navigate freely.",
	"Traversing the tree of a bygone filesystem state.",
	"The archive is your time machine. Where would you like to go?",
	"Browsing the depths. Every byte exactly as it was.",
	"The encrypted vault yields to your key. Explore.",
	"Inspecting the snapshot. The past is read-only.",
	"The tree unfolds. Your files await inspection.",
	"Reading the archive like a history book. Every file a chapter.",
	"The vault is open. The files are yours to examine.",
	"Peering through encrypted layers into your past.",
}

var emptyStateMessages = []string{
	"Nothing here yet. The vault awaits its first offering.",
	"Empty. Like the void, but with better error handling.",
	"No data to show. The archive hungers.",
	"The silence of an empty backup. Concerning.",
	"Nothing to display. Perhaps it's time for a backup?",
	"Void. Null. Empty. Your archive needs feeding.",
	"The vault echoes with emptiness. Time to change that.",
	"No snapshots found. The doomsday clock ticks louder.",
	"An empty archive is a vulnerability. Just saying.",
	"Zero snapshots. Zero safety net. Zero chill.",
	"Nothing here. The archive is a blank page, waiting.",
	"Empty state. The most dangerous state of all.",
	"No data preserved. Your future self is getting nervous.",
	"The vault is bare. This is not where you want to be.",
	"Congratulations. You have zero backups. Bold strategy.",
	"Nothing to see here. Which is exactly the problem.",
	"The archive is empty. The cosmos doesn't care. You should.",
	"No snapshots. No safety. No sleep tonight.",
	"Unprotected data detected. The archive recommends action.",
	"Empty. The word every backup tool dreads.",
	"No backups found. Living dangerously, are we?",
}

var scanningMessages = []string{
	"Scanning. Every file has a story to tell.",
	"Crawling the filesystem. Nothing escapes the scan.",
	"Inventorying your digital existence...",
	"Reading the filesystem like a surveillance report.",
	"Scanning for changes. The filesystem hides nothing.",
	"Every file inspected. Every modification noted.",
	"Traversing directories. The census continues.",
	"Cataloging your data. Every byte has a name.",
	"The scanner sees all. Changed, new, deleted.",
	"Walking the file tree. Branch by branch.",
	"Scanning in progress. Your filesystem is an open book.",
	"Documenting the state of your digital world.",
	"The filesystem confesses its changes under scanning.",
	"Enumerating files. The deduplication engine waits.",
	"Scanning for new recruits for the archive.",
	"Every directory entered. Every file measured.",
	"The scan finds what changed. The backup preserves it.",
	"Probing the filesystem. Resistance is futile.",
	"Reading directory entries like tea leaves.",
	"Your files are being counted. Every last one.",
	"The filesystem audit continues. Thoroughness is mandatory.",
}

var farewellMessages = []string{
	"The vault is sealed. Until next time.",
	"Shutting down. Your data knows the way out.",
	"Powering off. The dead man's switch is armed.",
	"Doomsday signing off. Stay paranoid.",
	"Goodbye. Your backups will keep watch while you're gone.",
	"Exiting. The archive stands guard in your absence.",
	"Logging off. The checksums will hold. Probably.",
	"Farewell. May your data outlive your hardware.",
	"Shutting down. The vault needs no operator.",
	"Until next time. The backups don't sleep.",
	"Signing off. Your data is in good hands. Its own.",
	"Powering down. The encrypted archive persists.",
	"Goodbye. Remember: paranoia is a survival trait.",
	"Exiting. The dead man's switch counts down.",
	"The terminal goes dark. The backups remain.",
	"Farewell. Your data fortress stands unattended but armed.",
	"Signing off. The bits endure in encrypted silence.",
	"Disconnecting. The archive requires no supervision.",
	"Doomsday out. The vault is self-sustaining.",
	"Goodbye. Sleep well knowing your data won't.",
	"Powering off. The backup sentries take over from here.",
}

// Greeting returns a random apocalyptic greeting message.
func Greeting() string {
	return pick(greetings)
}

// BackupStart returns a random message for when a backup begins.
func BackupStart() string {
	return pick(backupStartMessages)
}

// BackupComplete returns a random backup completion message.
func BackupComplete() string {
	return pick(backupCompleteMessages)
}

// IdleStatus returns a random idle/waiting status message.
func IdleStatus() string {
	return pick(idleStatusMessages)
}

// VersionTagline returns a random tagline for the version command.
func VersionTagline() string {
	return pick(versionTaglines)
}

// RestoreStart returns a random message for when a restore begins.
func RestoreStart() string {
	return pick(restoreStartMessages)
}

// RestoreComplete returns a random restore completion message.
func RestoreComplete() string {
	return pick(restoreCompleteMessages)
}

// BrowsingFiles returns a random message when entering the file browser.
func BrowsingFiles() string {
	return pick(browsingMessages)
}

// EmptyState returns a random message for empty states (no configs, no snapshots).
func EmptyState() string {
	return pick(emptyStateMessages)
}

// Scanning returns a random rotating status message during filesystem scans.
func Scanning() string {
	return pick(scanningMessages)
}

// Farewell returns a random exit/goodbye message.
func Farewell() string {
	return pick(farewellMessages)
}
