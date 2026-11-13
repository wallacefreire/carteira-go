// Package baixar grava um arquivo da internet no disco sem deixar arquivo
// pela metade: baixa para um temporário e só troca no fim.
package baixar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Modificado pergunta ao servidor (HEAD) quando o arquivo mudou pela última vez.
func Modificado(ctx context.Context, cli *http.Client, url string) (time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return time.Time{}, err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return time.Time{}, ErrNaoExiste
	}
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return http.ParseTime(resp.Header.Get("Last-Modified"))
}

// ErrNaoExiste indica resposta 404 (ex.: arquivo do ano que ainda não saiu).
var ErrNaoExiste = errors.New("arquivo não existe no servidor")

// Arquivo baixa url para destino.
func Arquivo(ctx context.Context, cli *http.Client, url, destino string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNaoExiste
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := destino + ".parcial"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, destino)
}
