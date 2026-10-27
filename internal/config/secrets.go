package config

import (
	"context"
	"errors"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// AWSSecrets reads Secrets Manager using the default AWS credential chain
// (the ECS task role in production). The client is created on first use, so
// runs that only use plain env vars never touch AWS.
type AWSSecrets struct {
	once   sync.Once
	client *secretsmanager.Client
	err    error
}

func (a *AWSSecrets) GetSecretString(ctx context.Context, id string) (string, error) {
	a.once.Do(func() {
		cfg, err := awsconfig.LoadDefaultConfig(ctx)
		if err != nil {
			a.err = err
			return
		}
		a.client = secretsmanager.NewFromConfig(cfg)
	})
	if a.err != nil {
		return "", a.err
	}
	out, err := a.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &id})
	if err != nil {
		return "", err
	}
	if out.SecretString == nil {
		return "", errors.New("secret has no SecretString")
	}
	return *out.SecretString, nil
}
