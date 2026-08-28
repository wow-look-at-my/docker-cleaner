package plan

import (
	"sort"
	"strings"

	"github.com/wow-look-at-my/docker-cleaner/internal/dockercli"
)

// selectImages keeps everything in use, the newest image of every repository,
// and anything tagged latest. What is left is a superseded version nobody can
// reach.
func (b *builder) selectImages() {
	sizes := map[string]int64{}
	for _, i := range b.snap.DiskUsage.Images {
		sizes[i.ID] = i.Size
	}

	images := append([]dockercli.Image(nil), b.snap.Images...)
	sort.Slice(images, func(i, j int) bool { return images[i].ID < images[j].ID })

	byID := map[string]dockercli.Image{}
	for _, img := range images {
		byID[img.ID] = img
	}

	newest, noTimestamps := newestPerRepo(images)
	latest := taggedLatest(images)

	keepReasons := map[string]Kept{}
	var candidates []dockercli.Image

	for _, img := range images {
		name := imageName(img)

		if st := b.refs.image(img.ID); st.held {
			keepReasons[img.ID] = Kept{KindImage, name, ReasonReferenced, strings.Join(st.holders, ", ")}
			continue
		}
		// A container's Config.Image is the reference as written, which can
		// now name a different id than the one the container actually runs.
		// It is used only to widen protection, never to grant it.
		if held := b.referencedByName(img); held != "" {
			keepReasons[img.ID] = Kept{KindImage, name, ReasonReferencedName, held}
			continue
		}
		if project, ok := b.composeImageProject(img); ok {
			keepReasons[img.ID] = Kept{KindImage, name, ReasonClaimedByCompos, project}
			continue
		}
		if reason, detail, ok := b.composeUnresolvedImage(img); ok {
			keepReasons[img.ID] = Kept{KindImage, name, reason, detail}
			continue
		}
		if latest[img.ID] {
			keepReasons[img.ID] = Kept{KindImage, name, ReasonTaggedLatest, ""}
			continue
		}
		if repo, ok := newest[img.ID]; ok {
			keepReasons[img.ID] = Kept{KindImage, name, ReasonNewestInRepo, repo}
			continue
		}
		if repo, ok := noTimestamps[img.ID]; ok {
			keepReasons[img.ID] = Kept{KindImage, name, ReasonNoTimestamps, repo}
			continue
		}
		if p := MatchKeep(b.opt.Keep, imageNames(img)); p != "" {
			keepReasons[img.ID] = Kept{KindImage, name, ReasonKeepPattern, p}
			continue
		}
		candidates = append(candidates, img)
	}

	candidates = b.protectParents(candidates, byID, keepReasons)

	for _, k := range sortedKept(keepReasons) {
		b.plan.Kept = append(b.plan.Kept, k)
	}

	for _, img := range candidates {
		if b.opt.SkipImages {
			b.keep(KindImage, imageName(img), ReasonStepDisabled, "")
			continue
		}
		b.plan.Images = append(b.plan.Images, Target{
			Kind:     KindImage,
			ID:       img.ID,
			Name:     imageName(img),
			Detail:   imageDetail(img),
			Note:     imageNote(img),
			Size:     sizes[img.ID],
			FreedBy:  b.refs.image(img.ID).by,
			Commands: removeImageCommands(img),
		})
	}
}

// newestPerRepo finds the image to keep in each repository. A repository whose
// images all lack a usable timestamp keeps every one of them: picking an
// arbitrary winner there would delete real versions on the strength of a
// coin flip.
func newestPerRepo(images []dockercli.Image) (newest, noTimestamps map[string]string) {
	type member struct {
		id   string
		when int64
		ok   bool
	}
	repos := map[string][]member{}

	for _, img := range images {
		for _, ref := range img.RepoTags {
			if ref == "" || strings.HasPrefix(ref, "<none>") {
				continue
			}
			repo, _ := SplitReference(ref)
			t, ok := dockercli.ParseTime(img.Created)
			repos[repo] = append(repos[repo], member{img.ID, t.UnixNano(), ok})
		}
	}

	newest = map[string]string{}
	noTimestamps = map[string]string{}
	for repo, members := range repos {
		best := member{}
		found := false
		for _, m := range members {
			if !m.ok {
				continue
			}
			// Ties break on id so the same input always yields the same plan.
			if !found || m.when > best.when || (m.when == best.when && m.id > best.id) {
				best, found = m, true
			}
		}
		if !found {
			for _, m := range members {
				noTimestamps[m.id] = repo
			}
			continue
		}
		newest[best.id] = repo
	}
	return newest, noTimestamps
}

func taggedLatest(images []dockercli.Image) map[string]bool {
	out := map[string]bool{}
	for _, img := range images {
		for _, ref := range img.RepoTags {
			if _, version := SplitReference(ref); version == "latest" {
				out[img.ID] = true
			}
		}
	}
	return out
}

// protectParents keeps any image another surviving image is built on. Docker
// refuses to remove one anyway, so listing it in the plan would promise a
// deletion that cannot happen. The loop repeats because a parent's parent must
// survive too.
func (b *builder) protectParents(candidates []dockercli.Image, byID map[string]dockercli.Image, kept map[string]Kept) []dockercli.Image {
	doomed := map[string]bool{}
	for _, img := range candidates {
		doomed[img.ID] = true
	}

	for changed := true; changed; {
		changed = false
		for id, img := range byID {
			if doomed[id] || img.Parent == "" || !doomed[img.Parent] {
				continue
			}
			doomed[img.Parent] = false
			kept[img.Parent] = Kept{KindImage, imageName(byID[img.Parent]), ReasonParentOfKept, ShortID(id)}
			changed = true
		}
	}

	var out []dockercli.Image
	for _, img := range candidates {
		if doomed[img.ID] {
			out = append(out, img)
		}
	}
	return out
}

// removeImageCommands removes each tag by reference. An id carrying several
// tags cannot be removed by id, and untagging only some of them would destroy
// a reference the user still has.
func removeImageCommands(img dockercli.Image) [][]string {
	var refs []string
	for _, ref := range img.RepoTags {
		if ref != "" && !strings.HasPrefix(ref, "<none>") {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return [][]string{dockercli.RemoveImageRef(img.ID)}
	}
	sort.Strings(refs)
	cmds := make([][]string, 0, len(refs))
	for _, ref := range refs {
		cmds = append(cmds, dockercli.RemoveImageRef(ref))
	}
	return cmds
}

func (b *builder) referencedByName(img dockercli.Image) string {
	for _, ref := range img.RepoTags {
		if st := b.refs.imageByName(ref); st.held {
			return strings.Join(st.holders, ", ")
		}
	}
	return ""
}

func imageName(img dockercli.Image) string {
	var refs []string
	for _, ref := range img.RepoTags {
		if ref != "" && !strings.HasPrefix(ref, "<none>") {
			refs = append(refs, ref)
		}
	}
	if len(refs) == 0 {
		return ShortID(img.ID) + " <untagged>"
	}
	sort.Strings(refs)
	return strings.Join(refs, ", ")
}

func imageNames(img dockercli.Image) []string {
	names := append([]string{img.ID, ShortID(img.ID)}, img.RepoTags...)
	return append(names, img.RepoDigests...)
}

func imageDetail(img dockercli.Image) string {
	if t, ok := dockercli.ParseTime(img.Created); ok {
		return "built " + t.Format("2006-01-02")
	}
	return "no build date"
}

// imageNote warns about a digest-pinned image. It has no tags, so it looks
// like a rebuild leftover, but a compose file may pin that digest and will
// re-pull it silently.
func imageNote(img dockercli.Image) string {
	if len(img.RepoTags) == 0 && len(img.RepoDigests) > 0 {
		return "digest-pinned: " + img.RepoDigests[0]
	}
	return ""
}

func sortedKept(m map[string]Kept) []Kept {
	out := make([]Kept, 0, len(m))
	for _, k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
