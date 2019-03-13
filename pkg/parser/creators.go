// Copyright 2019 Altinity Ltd and/or its affiliates. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package parser

import (
	"fmt"

	chiv1 "github.com/altinity/clickhouse-operator/pkg/apis/clickhouse.altinity.com/v1"

	"github.com/golang/glog"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateCHIObjects returns a map of the k8s objects created based on ClickHouseInstallation Object properties
// and slice of all full deployment ids
func CreateCHIObjects(chi *chiv1.ClickHouseInstallation, deploymentCountMax chiDeploymentCountMap) (ObjectsMap, []string) {
	var options genOptions

	options.deploymentCountMax = deploymentCountMax

	// Allocate data structures
	options.fullDeploymentIDToFingerprint = make(map[string]string)
	options.ssDeployments = make(map[string]*chiv1.ChiDeployment)

	// Config files section - macros and common config
	options.macrosData = make(map[string]macrosDataShardDescriptionList)

	// commonConfigSections maps section name to section XML config such as "<yandex><macros>...</macros><yandex>"
	// Bring in all sections into commonConfigSections[config file name]
	options.commonConfigSections = make(map[string]string)
	includeNonEmpty(options.commonConfigSections, filenameRemoteServersXML, generateRemoteServersConfig(chi, &options))
	includeNonEmpty(options.commonConfigSections, filenameZookeeperXML, generateZookeeperConfig(chi))
	includeNonEmpty(options.commonConfigSections, filenameUsersXML, generateUsersConfig(chi))
	includeNonEmpty(options.commonConfigSections, filenameProfilesXML, generateProfilesConfig(chi))
	includeNonEmpty(options.commonConfigSections, filenameQuotasXML, generateQuotasConfig(chi))
	includeNonEmpty(options.commonConfigSections, filenameSettingsXML, generateSettingsConfig(chi))

	// slice of full deployment ID's
	fullDeploymentIDs := make([]string, 0, len(options.fullDeploymentIDToFingerprint))
	for p := range options.fullDeploymentIDToFingerprint {
		fullDeploymentIDs = append(fullDeploymentIDs, p)
	}

	// Create k8s objects (data structures)
	return ObjectsMap{
		ObjectsServices: createServiceObjects(chi, &options),
		// Config Maps are of two types - common and personal
		ObjectsConfigMaps: createConfigMapObjects(chi, &options),
		// Config Maps are mapped as config files in Stateful Set objects
		ObjectsStatefulSets: createStatefulSetObjects(chi, &options),
	}, fullDeploymentIDs
}

// createConfigMapObjects returns a list of corev1.ConfigMap objects
func createConfigMapObjects(
	chi *chiv1.ClickHouseInstallation,
	options *genOptions,
) ConfigMapList {

	deploymentsCount := len(options.fullDeploymentIDToFingerprint)

	// There are two types configs, kept in ConfigMaps:
	// 1. Common configs - for all resources in the CHI (remote servers, zookeeper setup, etc)
	// 2. Personal configs - macros config
	// configMapList contains all configs
	configMapList := make(ConfigMapList, 1, deploymentsCount+1) // deploymentsCount for personal configs and +1 for common

	// ConfigMap common for all resources in CHI
	// contains several sections, mapped as separated config files, such as
	// remote servers, zookeeper setup, etc
	configMapList[0] = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CreateConfigMapCommonName(chi.Name),
			Namespace: chi.Namespace,
			Labels: map[string]string{
				ChopGeneratedLabel: chi.Name,
				CHIGeneratedLabel:  chi.Name,
			},
		},
		// Data contains several sections which are to be several xml configs
		Data: options.commonConfigSections,
	}

	// Each deployment has to have macros.xml config file
	for fullDeploymentID := range options.fullDeploymentIDToFingerprint {
		// Add corev1.ConfigMap object to the list
		configMapList = append(configMapList, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      CreateConfigMapMacrosName(chi.Name, fullDeploymentID),
				Namespace: chi.Namespace,
				Labels: map[string]string{
					ChopGeneratedLabel: chi.Name,
					CHIGeneratedLabel:  chi.Name,
				},
			},
			Data: map[string]string{
				filenameMacrosXML: generateHostMacros(chi.Name, fullDeploymentID, options.macrosData[fullDeploymentID]),
			},
		})
	}

	return configMapList
}

// createServiceObjects returns a list of corev1.Service objects
func createServiceObjects(chi *chiv1.ClickHouseInstallation, options *genOptions) ServiceList {
	serviceList := make(ServiceList, 0, 1+len(options.fullDeploymentIDToFingerprint))
	ports := []corev1.ServicePort{
		{
			Name: chDefaultRPCPortName,
			Port: chDefaultRPCPortNumber,
		},
		{
			Name: chDefaultInterServerPortName,
			Port: chDefaultInterServerPortNumber,
		},
		{
			Name: chDefaultRestPortName,
			Port: chDefaultRestPortNumber,
		},
	}

	serviceName := CreateChiServiceName(chi.Name)
	serviceList = append(serviceList, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: chi.Namespace,
			Labels: map[string]string{
				ChopGeneratedLabel: chi.Name,
				CHIGeneratedLabel:  chi.Name,
			},
		},
		Spec: corev1.ServiceSpec{
			// ClusterIP: templateDefaultsServiceClusterIP,
			Selector: map[string]string{
				CHIGeneratedLabel: chi.Name,
			},
			Ports: ports,
			Type: "LoadBalancer",
		},
	})
	glog.Infof("createServiceObjects() for service %s\n", serviceName)

	for fullDeploymentID := range options.fullDeploymentIDToFingerprint {
		statefulSetName := CreateStatefulSetName(fullDeploymentID)
		serviceName := CreatePodServiceName(fullDeploymentID)

		// Add corev1.Service object to the list
		serviceList = append(serviceList, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      serviceName,
				Namespace: chi.Namespace,
				Labels: map[string]string{
					ChopGeneratedLabel: chi.Name,
					CHIGeneratedLabel:  chi.Name,
				},
			},
			Spec: corev1.ServiceSpec{
				ClusterIP: templateDefaultsServiceClusterIP,
				Selector: map[string]string{
					chDefaultAppLabel: statefulSetName,
				},
				Ports: ports,
				Type: "ClusterIP",
			},
		})
		glog.Infof("createServiceObjects() for service %s\n", serviceName)
	}

	return serviceList
}

// createStatefulSetObjects returns a list of apps.StatefulSet objects
func createStatefulSetObjects(chi *chiv1.ClickHouseInstallation, options *genOptions) StatefulSetList {
	configMapCommonName := CreateConfigMapCommonName(chi.Name)
	statefulSetList := make(StatefulSetList, 0, len(options.fullDeploymentIDToFingerprint))

	// Templates index maps template name to (simplified) template itself
	// Used to provide names access to templates
	volumeClaimTemplatesIndex := createVolumeClaimTemplatesIndex(chi)
	podTemplatesIndex := createPodTemplatesIndex(chi)

	// List of mount points - do not allocate any items, they'll be appended
	commonVolumeMounts := make([]corev1.VolumeMount, 0)
	// Add common config volume mount at first
	// Personal macros would be added later
	for filename := range options.commonConfigSections {
		glog.Infof("commonVolumeMounts %s\n", filename)
		commonVolumeMounts = append(
			commonVolumeMounts, corev1.VolumeMount{
				Name:      configMapCommonName,
				MountPath: CreateClickHouseConfigFullPath(filename),
				SubPath:   filename,
			},
		)
	}
	glog.Infof("commonVolumeMounts total len %d\n", len(commonVolumeMounts))

	// Create list of apps.StatefulSet objects
	// StatefulSet is created for each Deployment
	for fullDeploymentID, fingerprint := range options.fullDeploymentIDToFingerprint {

		statefulSetName := CreateStatefulSetName(fullDeploymentID)
		serviceName := CreatePodServiceName(fullDeploymentID)
		configMapMacrosName := CreateConfigMapMacrosName(chi.Name, fullDeploymentID)
		volumeClaimTemplate := options.ssDeployments[fingerprint].VolumeClaimTemplate
		podTemplate := options.ssDeployments[fingerprint].PodTemplate

		// Copy list of shared corev1.VolumeMount objects into new slice
		commonVolumeMountsNum := len(commonVolumeMounts)
		currentVolumeMounts := make([]corev1.VolumeMount, commonVolumeMountsNum)
		// Prepare volume mount for current deployment as common mounts + personal mounts
		copy(currentVolumeMounts, commonVolumeMounts)
		// Personal volume mounts
		// Appending "macros.xml" section
		currentVolumeMounts = append(currentVolumeMounts, corev1.VolumeMount{
			Name:      configMapMacrosName,
			MountPath: CreateClickHouseConfigFullPath(filenameMacrosXML),
			SubPath:   filenameMacrosXML,
		})

		glog.Infof("Deployment %s has %d current volume mounts\n", fullDeploymentID, len(currentVolumeMounts))

		// Create apps.StatefulSet object
		replicasNum := int32(1)
		statefulSetObject := &apps.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      statefulSetName,
				Namespace: chi.Namespace,
				Labels: map[string]string{
					ChopGeneratedLabel: chi.Name,
					CHIGeneratedLabel:  chi.Name,
				},
			},
			Spec: apps.StatefulSetSpec{
				Replicas:    &replicasNum,
				ServiceName: serviceName,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						chDefaultAppLabel: statefulSetName,
					},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Name: statefulSetName,
						Labels: map[string]string{
							chDefaultAppLabel:  statefulSetName,
							ChopGeneratedLabel: chi.Name,
							CHIGeneratedLabel:  chi.Name,
						},
					},
					Spec: corev1.PodSpec{
						Volumes:    []corev1.Volume{},
						Containers: []corev1.Container{},
					},
				},
			},
		}

		// Specify pod templates - either explicitly defined or default
		if podTemplateData, ok := podTemplatesIndex[podTemplate]; ok {
			// Prepare .statefulSetObject.Spec.Template.Spec - fill with template's data

			// Copy volumes from pod template
			statefulSetObject.Spec.Template.Spec.Volumes = make([]corev1.Volume, len(podTemplateData.volumes))
			copy(statefulSetObject.Spec.Template.Spec.Volumes, podTemplateData.volumes)

			// Copy containers from pod template
			// ... snippet from .spec.templates.podTemplates
			//       containers:
			//      - name: clickhouse
			//        volumeMounts:
			//        - name: clickhouse-data-test
			//          mountPath: /var/lib/clickhouse
			//        image: yandex/clickhouse-server:18.16.2
			statefulSetObject.Spec.Template.Spec.Containers = make([]corev1.Container, len(podTemplateData.containers))
			copy(statefulSetObject.Spec.Template.Spec.Containers, podTemplateData.containers)

			// And now loop over all containers in this template and append all current VolumeMounts
			for i := range statefulSetObject.Spec.Template.Spec.Containers {
				for j := range currentVolumeMounts {
					statefulSetObject.Spec.Template.Spec.Containers[i].VolumeMounts = append(
						statefulSetObject.Spec.Template.Spec.Containers[i].VolumeMounts,
						currentVolumeMounts[j],
					)
				}
			}
			glog.Infof("createStatefulSetObjects() for statefulSet %s - template: %s\n", statefulSetName, podTemplate)
		} else {
			// No pod template specified for this deployment - use default container template
			// 1. No Volumes specified
			// 2. Containers are specified
			statefulSetObject.Spec.Template.Spec.Containers = append(
				statefulSetObject.Spec.Template.Spec.Containers,
				createDefaultContainerTemplate(chi, statefulSetName, currentVolumeMounts),
			)
			glog.Infof("createStatefulSetObjects() for statefulSet %s - default template\n", statefulSetName)
		}

		// Add configMaps as Pod's volumes
		// Both common and personal
		statefulSetObject.Spec.Template.Spec.Volumes = append(
			statefulSetObject.Spec.Template.Spec.Volumes,
			createVolume(configMapCommonName),
		)
		statefulSetObject.Spec.Template.Spec.Volumes = append(
			statefulSetObject.Spec.Template.Spec.Volumes,
			createVolume(configMapMacrosName),
		)

		// Specify volume claim templates - either explicitly defined or default
		if volumeClaimTemplateData, ok := volumeClaimTemplatesIndex[volumeClaimTemplate]; ok {
			statefulSetObject.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
				*volumeClaimTemplatesIndex[volumeClaimTemplate].persistentVolumeClaim,
			}

			// Add default corev1.VolumeMount section for ClickHouse data
			if volumeClaimTemplateData.useDefaultName {
				statefulSetObject.Spec.Template.Spec.Containers[0].VolumeMounts = append(
					statefulSetObject.Spec.Template.Spec.Containers[0].VolumeMounts,
					corev1.VolumeMount{
						Name:      chDefaultVolumeMountNameData,
						MountPath: fullPathClickHouseData,
					})
			}
			glog.Infof("createStatefulSetObjects() for statefulSet %s - vc template: %s\n", statefulSetName, volumeClaimTemplate)
		}

		// Append apps.StatefulSet to the list of stateful sets
		statefulSetList = append(statefulSetList, statefulSetObject)
		glog.Infof("createStatefulSetObjects() for statefulSet %s\n", statefulSetName)
	}

	return statefulSetList
}

// createVolume returns corev1.Volume object with defined name
func createVolume(name string) corev1.Volume {
	return corev1.Volume{
		Name: name,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
			},
		},
	}
}

// createDefaultContainerTemplate returns default corev1.Container object
func createDefaultContainerTemplate(
	chi *chiv1.ClickHouseInstallation,
	name string,
	volumeMounts []corev1.VolumeMount,
) corev1.Container {
	return corev1.Container{
		Name:  name,
		Image: chDefaultDockerImage,
		Ports: []corev1.ContainerPort{
			{
				Name:          chDefaultRPCPortName,
				ContainerPort: chDefaultRPCPortNumber,
			},
			{
				Name:          chDefaultInterServerPortName,
				ContainerPort: chDefaultInterServerPortNumber,
			},
			{
				Name:          chDefaultRestPortName,
				ContainerPort: chDefaultRestPortNumber,
			},
		},
		VolumeMounts: volumeMounts,
	}
}

// createVolumeClaimTemplatesIndex returns a map of vcTemplatesIndexData used as a reference storage for VolumeClaimTemplates
func createVolumeClaimTemplatesIndex(chi *chiv1.ClickHouseInstallation) vcTemplatesIndex {
	index := make(vcTemplatesIndex)
	for i := range chi.Spec.Templates.VolumeClaimTemplates {
		// Convenience wrapper
		volumeClaimTemplate := &chi.Spec.Templates.VolumeClaimTemplates[i]

		flag := false
		if volumeClaimTemplate.PersistentVolumeClaim.Name == useDefaultNamePlaceholder {
			volumeClaimTemplate.PersistentVolumeClaim.Name = chDefaultVolumeMountNameData
			flag = true
		}
		index[volumeClaimTemplate.Name] = &vcTemplatesIndexData{
			useDefaultName:        flag,
			persistentVolumeClaim: &volumeClaimTemplate.PersistentVolumeClaim,
		}
	}
	return index
}

// createPodTemplatesIndex returns a map of podTemplatesIndexData used as a reference storage for PodTemplates
func createPodTemplatesIndex(chi *chiv1.ClickHouseInstallation) podTemplatesIndex {
	index := make(podTemplatesIndex)
	for i := range chi.Spec.Templates.PodTemplates {
		// Convenience wrapper
		podTemplate := &chi.Spec.Templates.PodTemplates[i]
		index[podTemplate.Name] = &podTemplatesIndexData{
			containers: podTemplate.Containers,
			volumes:    podTemplate.Volumes,
		}
	}

	return index
}

// CreateConfigMapMacrosName returns a name for a ConfigMap (CH macros) resource based on predefined pattern
func CreateConfigMapMacrosName(chiName, prefix string) string {
	return fmt.Sprintf(configMapMacrosNamePattern, chiName, prefix)
}

// CreateConfigMapCommonName returns a name for a ConfigMap resource based on predefined pattern
func CreateConfigMapCommonName(chiName string) string {
	return fmt.Sprintf(configMapCommonNamePattern, chiName)
}

// CreatePodServiceName creates a name of a pod Service resource
// prefix is a fullDeploymentID
func CreatePodServiceName(prefix string) string {
	return fmt.Sprintf(podServiceNamePattern, prefix)
}

// CreateInstServiceName creates a name of a Installation Service resource
// prefix is a fullDeploymentID
func CreateChiServiceName(prefix string) string {
	return fmt.Sprintf(chiServiceNamePattern, prefix)
}

// CreateStatefulSetName creates a name of a StatefulSet resource
// prefix is a fullDeploymentID
func CreateStatefulSetName(prefix string) string {
	return fmt.Sprintf(statefulSetNamePattern, prefix)
}

// CreatePodHostname creates a name of a Pod resource
// prefix is a fullDeploymentID
// ss-1eb454-2-0
func CreatePodHostname(prefix string) string {
	return fmt.Sprintf(podHostnamePattern, prefix)
}

// CreateNamespaceDomainName creates domain name of a namespace
// .my-dev-namespace.svc.cluster.local
func CreateNamespaceDomainName(chiNamespace string) string {
	return fmt.Sprintf(namespaceDomainPattern, chiNamespace)
}

// CreatePodFQDN creates a fully qualified domain name of a pod
// prefix is a fullDeploymentID
// ss-1eb454-2-0.my-dev-domain.svc.cluster.local
func CreatePodFQDN(chiNamespace, prefix string) string {
	return fmt.Sprintf(
		podFQDNPattern,
		prefix,
		chiNamespace,
	)
}

// CreateClickHouseConfigFullPath create full path to ClickHouse config file
func CreateClickHouseConfigFullPath(filename string) string {
	return fmt.Sprintf(fullPathConfigTemplate, filename)
}