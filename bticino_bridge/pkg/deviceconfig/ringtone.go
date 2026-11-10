package deviceconfig

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"

	"bticino_bridge/pkg/openwebnet"
)

// RingtonesXMLPath lists the ringtones the device offers (ring<N>.wav).
var RingtonesXMLPath = "/home/bticino/ringtones/ringtones.xml"

// SettableRingtones maps the ringtone types of the device menu to their
// settings.xml object id, the ids the native GUI and bt_answering_machine use.
var SettableRingtones = map[string]string{
	"s0":       "14101",
	"s1":       "14102",
	"internal": "14105",
	"external": "14106",
	"floor":    "14107",
}

// reloadRingtonesFrame makes bt_answering_machine reread settings.xml and
// relink /var/tmp/ringtones/*.wav, the files bt_av_media plays.
const reloadRingtonesFrame = "*8*99*##"

// ErrInvalidRingtone marks an unknown ringtone type or id.
var ErrInvalidRingtone = errors.New("invalid ringtone")

// sendReload is replaced in tests.
var sendReload = func() error {
	_, err := openwebnet.SendCommandSession(reloadRingtonesFrame)
	return err
}

var setRingtoneMu sync.Mutex

// RingtoneFile is one entry of ringtones.xml.
type RingtoneFile struct {
	ID   int    `json:"id"`
	File string `json:"file"`
}

// ReadRingtoneList reads the available ringtones from ringtones.xml.
func ReadRingtoneList(path string) ([]RingtoneFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Items []struct {
			ID   int    `xml:"id_ringtone"`
			Path string `xml:"path_ringtone"`
		} `xml:"ringtones>item"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse ringtones.xml: %w", err)
	}
	list := make([]RingtoneFile, 0, len(doc.Items))
	for _, item := range doc.Items {
		list = append(list, RingtoneFile{ID: item.ID, File: item.Path})
	}
	return list, nil
}

// RingtoneFilePath returns the file of a ringtone listed in ringtones.xml.
func RingtoneFilePath(id int) (string, error) {
	list, err := ReadRingtoneList(RingtonesXMLPath)
	if err != nil {
		return "", err
	}
	for _, r := range list {
		if r.ID == id {
			return filepath.Join(filepath.Dir(RingtonesXMLPath), filepath.Base(r.File)), nil
		}
	}
	return "", fmt.Errorf("%w: no ringtone %d", ErrInvalidRingtone, id)
}

// SettableValues returns the current ringtone id of each settable type.
func (r Ringtones) SettableValues() map[string]int {
	return map[string]int{
		"s0":       r.S0,
		"s1":       r.S1,
		"internal": r.Internal,
		"external": r.External,
		"floor":    r.Door,
	}
}

// SetRingtone writes the ringtone into settings.xml and makes the device
// reload it. The native GUI keeps its own copy: its menu shows the old value
// until the next restart, and saving in that menu writes its copy back.
func SetRingtone(ringtoneType string, id int) error {
	list, err := ReadRingtoneList(RingtonesXMLPath)
	if err != nil {
		return err
	}
	if !containsRingtone(list, id) {
		return fmt.Errorf("%w: no ringtone %d", ErrInvalidRingtone, id)
	}

	setRingtoneMu.Lock()
	defer setRingtoneMu.Unlock()
	if err := WriteRingtone(DevicePaths.SettingsXML, ringtoneType, id); err != nil {
		return err
	}
	if err := sendReload(); err != nil {
		return fmt.Errorf("reload ringtones: %w", err)
	}
	return nil
}

func containsRingtone(list []RingtoneFile, id int) bool {
	for _, r := range list {
		if r.ID == id {
			return true
		}
	}
	return false
}

var istIDRingtone = regexp.MustCompile(`\bid_ringtone="[^"]*"`)

// WriteRingtone sets id_ringtone on the <ist> of the ringtone's object and
// leaves every other byte of the file as it is. The file is replaced by a
// rename with the original owner and mode, as the native GUI does it.
func WriteRingtone(path, ringtoneType string, id int) error {
	objectID, ok := SettableRingtones[ringtoneType]
	if !ok {
		return fmt.Errorf("%w: unknown type %q", ErrInvalidRingtone, ringtoneType)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	obj := regexp.MustCompile(`<obj\b[^>]*\bcid="` + objectID + `"[^>]*>`).FindIndex(data)
	if obj == nil {
		return fmt.Errorf("ringtone object %s not found in %s", objectID, path)
	}
	end := regexp.MustCompile(`</obj>`).FindIndex(data[obj[1]:])
	if end == nil {
		return fmt.Errorf("ringtone object %s is not closed in %s", objectID, path)
	}
	body := data[obj[1] : obj[1]+end[0]]
	ist := regexp.MustCompile(`<ist\b[^>]*>`).FindIndex(body)
	if ist == nil {
		return fmt.Errorf("ringtone object %s has no <ist> in %s", objectID, path)
	}

	tag := body[ist[0]:ist[1]]
	attr := fmt.Sprintf(`id_ringtone="%d"`, id)
	var newTag []byte
	if istIDRingtone.Match(tag) {
		newTag = istIDRingtone.ReplaceAll(tag, []byte(attr))
	} else {
		newTag = append([]byte(`<ist `+attr), tag[len("<ist"):]...)
	}

	tagStart := obj[1] + ist[0]
	out := make([]byte, 0, len(data)+len(attr))
	out = append(out, data[:tagStart]...)
	out = append(out, newTag...)
	out = append(out, data[tagStart+len(tag):]...)
	return replaceFile(path, out)
}

// replaceFile writes data next to path and renames it over path, keeping the
// owner and mode: bt_answering_machine reads the file as user bticino.
func replaceFile(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bridge-settings-*.xml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := os.Chown(tmp.Name(), int(st.Uid), int(st.Gid)); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), path)
}
