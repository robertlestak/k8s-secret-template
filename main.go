package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

var (
	k8sClient *kubernetes.Clientset
)

// createKubeClient creates a global k8s client
func createKubeClient() error {
	l := log.WithFields(
		log.Fields{
			"action": "createKubeClient",
		},
	)
	l.Print("get createKubeClient")
	var kubeconfig string
	var err error
	if os.Getenv("KUBECONFIG") != "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	} else if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	var config *rest.Config
	// naïvely assume if no kubeconfig file that we are running in cluster
	if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
		config, err = rest.InClusterConfig()
		if err != nil {
			l.Printf("res.InClusterConfig error=%v", err)
			return err
		}
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			l.Printf("clientcmd.BuildConfigFromFlags error=%v", err)
			return err
		}
	}
	k8sClient, err = kubernetes.NewForConfig(config)
	if err != nil {
		l.Printf("kubernetes.NewForConfig error=%v", err)
		return err
	}
	return nil
}

// getSecrets returns all sync-enabled secrets managed by the cert-manager-sync operator
func getSecrets(ns string) ([]corev1.Secret, error) {
	var slo []corev1.Secret
	var err error
	l := log.WithFields(
		log.Fields{
			"action": "getSecrets",
		},
	)
	l.Print("get secrets")
	sc := k8sClient.CoreV1().Secrets(ns)
	lo := &metav1.ListOptions{}
	sl, jerr := sc.List(context.Background(), *lo)
	if jerr != nil {
		l.Printf("list error=%v", jerr)
		return slo, jerr
	}
	l.Printf("range secrets: %d", len(sl.Items))
	slo = append(slo, sl.Items...)
	return slo, err
}

func getSecretFiles(dir string) []string {
	files, err := ioutil.ReadDir(dir)
	if err != nil {
		log.Errorf("Failed to read directory: %s", err)
		return nil
	}

	var secretFiles []string
	for _, file := range files {
		if !file.IsDir() {
			secretFiles = append(secretFiles, path.Join(dir, file.Name()))
		}
	}

	return secretFiles
}

func removeComments(doc string) string {
	lines := strings.Split(doc, "\n")
	var result []string
	for _, line := range lines {
		if strings.HasPrefix(line, "#") {
			continue
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

func parseFilesAsSecrets(files []string) ([]*corev1.Secret, error) {
	l := log.WithFields(
		log.Fields{
			"action": "parseFilesAsSecrets",
			"files":  len(files),
		})
	l.Print("parseFilesAsSecrets")
	var secrets []*corev1.Secret
	for _, file := range files {
		l.Printf("file: %s", file)
		fd, ferr := ioutil.ReadFile(file)
		if ferr != nil {
			log.Errorf("Failed to read file: %s", ferr)
			return nil, ferr
		}
		docs := strings.Split(string(fd), "---")
		for _, doc := range docs {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			doc = removeComments(doc)
			if strings.TrimSpace(doc) == "" {
				continue
			}
			decode := scheme.Codecs.UniversalDeserializer().Decode
			object, _, err := decode([]byte(doc), nil, nil)
			if err != nil {
				log.Errorf("Failed to decode secret: %s", err)
				return nil, err
			}
			if object.GetObjectKind().GroupVersionKind() == corev1.SchemeGroupVersion.WithKind("Secret") {
				s, ok := object.(*corev1.Secret)
				if !ok {
					l.Printf("object is not a secret")
					return nil, fmt.Errorf("unexpected object type: %T", object)
				}
				l.Printf("secret: %s/%s", s.Namespace, s.Name)
				secrets = append(secrets, s)
			}
		}

	}
	return secrets, nil
}

func secretNamespaces(secrets []*corev1.Secret) []string {
	var namespaces []string
secretsLoop:
	for _, secret := range secrets {
		for _, n := range namespaces {
			if secret.Namespace == n {
				continue secretsLoop
			}
		}
		namespaces = append(namespaces, secret.Namespace)
	}
	return namespaces
}

func mergeMapStringString(annotations map[string]string, annotationsToMerge map[string]string) map[string]string {
	for k, v := range annotationsToMerge {
		annotations[k] = v
	}
	return annotations
}

func updateSecretData(newSecrets []*corev1.Secret, existingSecrets []corev1.Secret) ([]*corev1.Secret, error) {
	l := log.WithFields(
		log.Fields{
			"action": "updateSecretData",
			"new":    len(newSecrets),
			"old":    len(existingSecrets),
		})
	l.Print("updateSecretData")
newLoop:
	for i, ns := range newSecrets {
		l.Printf("new secret: %d/%d", ns.Namespace, ns.Name)
		for _, es := range existingSecrets {
			l.Printf("existing secret: %s/%s", es.Namespace, es.Name)
			if ns.Name == es.Name && ns.Namespace == es.Namespace {
				l.Printf("update secret: %s/%s", ns.Namespace, ns.Name)
				a := mergeMapStringString(es.Annotations, newSecrets[i].Annotations)
				lb := mergeMapStringString(es.Labels, newSecrets[i].Labels)
				newSecrets[i] = &es
				newSecrets[i].Annotations = a
				newSecrets[i].Labels = lb
				continue newLoop
			}
		}
	}
	return newSecrets, nil
}

func deleteRecreateSecret(secret *corev1.Secret) error {
	l := log.WithFields(
		log.Fields{
			"action": "deleteRecreateSecret",
			"secret": secret.Namespace + "/" + secret.Name,
		},
	)
	l.Print("deleteRecreateSecret")
	secret.ResourceVersion = ""
	secret.UID = ""
	secret.CreationTimestamp = metav1.Time{
		Time: time.Time{},
	}
	sc := k8sClient.CoreV1().Secrets(secret.Namespace)
	_, err := sc.Get(context.Background(), secret.Name, metav1.GetOptions{})
	if err != nil {
		l.Printf("secret does not exist: %s", err)
		return err
	}
	derr := sc.Delete(context.Background(), secret.Name, metav1.DeleteOptions{})
	if derr != nil {
		l.Printf("delete error: %s", derr)
		return derr
	}
	_, err = sc.Create(context.Background(), secret, metav1.CreateOptions{})
	if err != nil {
		l.Printf("create error: %s", err)
		return err
	}
	return nil
}

func ensureCreateSecret(secret *corev1.Secret) error {
	l := log.WithFields(
		log.Fields{
			"action": "ensureCreateSecret",
			"ns":     secret.Namespace,
			"name":   secret.Name,
		},
	)
	l.Print("ensureCreateSecret")
	if secret.UID != "" {
		l.Printf("secret UID: %s/%s %s", secret.Namespace, secret.Name, secret.UID)
		s, err := k8sClient.CoreV1().Secrets(secret.Namespace).Update(context.Background(), secret, metav1.UpdateOptions{
			//DryRun: []string{"All"},
		})
		if err != nil {
			l.Printf("update error: %v", err)
			return deleteRecreateSecret(secret)
		}
		l.Printf("updated secret: %s/%s", s.Namespace, s.Name)
	} else {
		l.Printf("secret: %s/%s", secret.Namespace, secret.Name)
		s, err := k8sClient.CoreV1().Secrets(secret.Namespace).Create(context.Background(), secret, metav1.CreateOptions{
			//DryRun: []string{"All"},
		})
		if err != nil {
			l.Printf("create error: %v", err)
			return deleteRecreateSecret(secret)
		}
		l.Printf("created secret: %s/%s", s.Namespace, s.Name)
	}
	return nil
}

func updateK8sSecrets(secrets []*corev1.Secret) error {
	l := log.WithFields(
		log.Fields{
			"action":  "updateK8sSecrets",
			"secrets": len(secrets),
		})
	l.Print("updateK8sSecrets")
	for _, secret := range secrets {
		l.Printf("secret: %s/%s %s", secret.Namespace, secret.Name, secret.UID)
		err := ensureCreateSecret(secret)
		if err != nil {
			l.Printf("error: %v", err)
			return err
		}
	}
	return nil
}

func init() {
	l := log.WithFields(
		log.Fields{
			"action": "init",
		},
	)
	l.Print("init")
	cerr := createKubeClient()
	if cerr != nil {
		l.Fatal(cerr)
	}
}

func main() {
	l := log.WithFields(log.Fields{
		"module": "main",
	})
	l.Info("starting")
	secretDir := os.Getenv("SECRETS_DIR")
	if secretDir == "" && len(os.Args) > 1 {
		secretDir = os.Args[1]
	}
	secretFiles := getSecretFiles(secretDir)
	sec, err := parseFilesAsSecrets(secretFiles)
	if err != nil {
		l.Fatal(err)
	}
	l.Printf("parsed secrets: %d", len(sec))
	nsc := secretNamespaces(sec)
	var allSecrets []corev1.Secret
	for _, ns := range nsc {
		l.Printf("get existing secrets in namespace: %s", ns)
		s, err := getSecrets(ns)
		if err != nil {
			l.Fatal(err)
		}
		l.Printf("secrets: %d", len(s))
		allSecrets = append(allSecrets, s...)
	}
	l.Printf("all existing secrets: %d", len(allSecrets))
	us, uerr := updateSecretData(sec, allSecrets)
	if uerr != nil {
		l.Fatal(uerr)
	}
	l.Printf("updated secrets: %+v", len(us))
	uerr = updateK8sSecrets(us)
	if uerr != nil {
		l.Fatal(uerr)
	}
	l.Info("done")
}
