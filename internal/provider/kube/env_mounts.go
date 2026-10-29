package kube

import (
	"reflect"
	"sort"
	"strings"

	"github.com/gkdis6/opsgate/internal/redact"
	corev1 "k8s.io/api/core/v1"
)

func (v *envView) mounts(c corev1.Container, volumes []corev1.Volume) error {
	for _, mount := range c.VolumeMounts {
		var volume *corev1.Volume
		for i := range volumes {
			if volumes[i].Name == mount.Name {
				volume = &volumes[i]
				break
			}
		}
		if volume == nil {
			v.line("  %s (volume %s not found)", mount.MountPath, mount.Name)
			continue
		}
		mode := ""
		if mount.ReadOnly {
			mode = ", read-only"
		}
		switch {
		case volume.ConfigMap != nil:
			ref := volume.ConfigMap
			v.line("  %s (ConfigMap %s, read-only)%s", mount.MountPath, ref.Name, optionalEnv(ref.Optional))
			if err := v.mapFiles(ref.Name, ref.Items, mount, ref.Optional); err != nil {
				return err
			}
		case volume.Secret != nil:
			v.line("  %s (Secret %s) keys not shown%s", mount.MountPath, volume.Secret.SecretName, optionalEnv(volume.Secret.Optional))
			v.secretItems(volume.Secret.Items, mount)
		case volume.Projected != nil:
			v.line("  %s (projected, read-only)", mount.MountPath)
			for _, source := range volume.Projected.Sources {
				switch {
				case source.ConfigMap != nil:
					ref := source.ConfigMap
					v.line("    ConfigMap %s%s", ref.Name, optionalEnv(ref.Optional))
					if err := v.mapFiles(ref.Name, ref.Items, mount, ref.Optional); err != nil {
						return err
					}
				case source.Secret != nil:
					v.line("    Secret %s; keys not shown%s", source.Secret.Name, optionalEnv(source.Secret.Optional))
					v.secretItems(source.Secret.Items, mount)
				case source.DownwardAPI != nil:
					v.line("    downwardAPI")
				case source.ServiceAccountToken != nil:
					v.line("    serviceAccountToken (content not read)")
				case source.ClusterTrustBundle != nil:
					v.line("    clusterTrustBundle (content not read)")
				case source.PodCertificate != nil:
					v.line("    podCertificate (content not read)")
				}
			}
		case volume.PersistentVolumeClaim != nil:
			v.line("  %s (PersistentVolumeClaim %s%s)", mount.MountPath, volume.PersistentVolumeClaim.ClaimName, mode)
		case volume.EmptyDir != nil:
			v.line("  %s (emptyDir)", mount.MountPath)
		case volume.HostPath != nil:
			v.line("  %s (hostPath %s%s)", mount.MountPath, volume.HostPath.Path, mode)
		case volume.DownwardAPI != nil:
			v.line("  %s (downwardAPI)", mount.MountPath)
		case volume.CSI != nil:
			v.line("  %s (csi driver %s%s)", mount.MountPath, volume.CSI.Driver, mode)
		case volume.NFS != nil:
			v.line("  %s (nfs %s:%s%s)", mount.MountPath, volume.NFS.Server, volume.NFS.Path, mode)
		case volume.Ephemeral != nil:
			v.line("  %s (ephemeral)", mount.MountPath)
		default:
			v.line("  %s (%s)", mount.MountPath, envVolumeSource(volume.VolumeSource))
		}
	}
	return nil
}

func envMountPath(path string, mount corev1.VolumeMount) bool {
	return mount.SubPathExpr == "" && (mount.SubPath == "" || path == mount.SubPath)
}

func (v *envView) secretItems(items []corev1.KeyToPath, mount corev1.VolumeMount) {
	for _, item := range items {
		if envMountPath(item.Path, mount) {
			v.line("    %s (declared key %s; content not read)", item.Path, item.Key)
		}
	}
	if mount.SubPath != "" {
		v.line("    subPath: %s", mount.SubPath)
	}
	if mount.SubPathExpr != "" {
		v.line("    subPathExpr: %s (not resolved)", mount.SubPathExpr)
	}
}

func (v *envView) mapFiles(name string, items []corev1.KeyToPath, mount corev1.VolumeMount, optional *bool) error {
	cm, err := v.configMap(name)
	if err != nil {
		return err
	}
	if cm == nil {
		v.line("    ConfigMap %s not found%s", name, optionalEnv(optional))
		return nil
	}
	if mount.SubPathExpr != "" {
		v.line("    subPathExpr: %s (not resolved)", mount.SubPathExpr)
		return nil
	}
	if len(items) == 0 {
		var keys []string
		for key := range cm.Data {
			keys = append(keys, key)
		}
		for key := range cm.BinaryData {
			if _, ok := cm.Data[key]; !ok {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			items = append(items, corev1.KeyToPath{Key: key, Path: key})
		}
	}
	found := false
	for _, item := range items {
		if !envMountPath(item.Path, mount) {
			continue
		}
		found = true
		data, text := cm.Data[item.Key]
		binary, binaryFound := cm.BinaryData[item.Key]
		if !text && !binaryFound {
			v.line("    %s (key %s not found)%s", item.Path, item.Key, optionalEnv(optional))
			continue
		}
		size := len(data)
		if !text {
			size = len(binary)
		}
		v.line("    %s (%d bytes)", item.Path, size)
		switch {
		case sensitiveEnv.MatchString(item.Key):
			v.line("      <hidden>")
		case !v.p.cfg.ShowConfigMapData:
			v.line("      content hidden; show_configmap_data is off")
		case !text:
			v.line("      binary data; size only")
		case v.contentBytes >= 64<<10:
			v.line("      size only; 64 KiB ConfigMap content limit reached")
		default:
			// Redact before cutting bytes so truncation cannot reveal a partial token.
			safe := redact.String(data)
			limit := min(8<<10, (64<<10)-v.contentBytes)
			shown := min(len(safe), limit)
			for _, line := range strings.Split(safe[:shown], "\n") {
				v.line("      %s", line)
			}
			v.contentBytes += shown
			if shown < len(safe) {
				v.line("      [truncated: %d of %d redacted bytes shown; 8 KiB per key, 64 KiB per call]", shown, len(safe))
			}
		}
	}
	if !found && mount.SubPath != "" {
		v.line("    subPath %s not found", mount.SubPath)
	}
	return nil
}

func envVolumeSource(source corev1.VolumeSource) string {
	fields := reflect.ValueOf(source)
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Field(i)
		if field.IsNil() {
			continue
		}
		name := strings.Split(fields.Type().Field(i).Tag.Get("json"), ",")[0]
		for _, key := range []string{"VolumeID", "PDName", "DiskName", "ShareName", "VolumePath", "Image", "DatasetName", "VolumeName"} {
			value := field.Elem().FieldByName(key)
			if value.IsValid() && value.Kind() == reflect.String && value.String() != "" {
				name += " " + value.String()
				break
			}
		}
		return name
	}
	return "unknown volume source"
}
