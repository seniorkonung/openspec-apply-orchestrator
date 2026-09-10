//go:build paseo_integration

package testpaseo

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Binaries struct {
	providerPath string
	proxyPath    string
}

func BuildBinaries(ctx context.Context, moduleRoot, outputRoot string) (Binaries, error) {
	if err := ctx.Err(); err != nil {
		return Binaries{}, err
	}
	binaries := Binaries{
		providerPath: filepath.Join(outputRoot, "test-provider"),
		proxyPath:    filepath.Join(outputRoot, "paseoproxy", "paseo"),
	}
	if err := os.MkdirAll(filepath.Dir(binaries.proxyPath), 0o700); err != nil {
		return Binaries{}, fmt.Errorf("создать каталог helper-бинарников: %w", err)
	}
	for _, target := range []struct {
		path        string
		packagePath string
	}{
		{path: binaries.providerPath, packagePath: "./internal/paseo/testpaseo/cmd/provider"},
		{path: binaries.proxyPath, packagePath: "./internal/paseo/testpaseo/cmd/paseoproxy"},
	} {
		if err := buildBinary(ctx, moduleRoot, target.path, target.packagePath); err != nil {
			return Binaries{}, err
		}
		if err := os.Chmod(target.path, 0o500); err != nil {
			return Binaries{}, fmt.Errorf("сделать helper-бинарник неизменяемым %s: %w", target.path, err)
		}
		if err := ctx.Err(); err != nil {
			return Binaries{}, err
		}
	}
	return binaries, nil
}

func (binaries Binaries) validate() error {
	for name, path := range map[string]string{
		"test-provider": binaries.providerPath,
		"paseoproxy":    binaries.proxyPath,
	} {
		information, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("прочитать helper-бинарник %s: %w", name, err)
		}
		if !information.Mode().IsRegular() || information.Mode().Perm()&0o100 == 0 {
			return fmt.Errorf("helper-бинарник %s не является исполняемым обычным файлом", name)
		}
	}
	return nil
}

func buildBinary(ctx context.Context, moduleRoot, outputPath, packagePath string) error {
	var output bytes.Buffer
	command := exec.Command("go", "build", "-tags=paseo_integration", "-o", outputPath, packagePath)
	command.Dir = moduleRoot
	command.Stdout = &output
	command.Stderr = &output
	if err := RunOwnedCommand(ctx, command); err != nil {
		return fmt.Errorf("собрать %s: %w\n%s", packagePath, err, output.Bytes())
	}
	return nil
}
