package proton_api_bridge

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"

	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/ProtonMail/go-proton-api"
)

// DownloadThumbnail fetches and decrypts a photo's rendered preview. The
// thumbnail is stored as an encrypted block (EncryptAndSign'ed with the
// revision's content session key), so the full path is: locate the block via
// POST /drive/volumes/{volumeID}/thumbnails, download it, verify its hash, then
// DecryptAndVerify -- exactly matching Proton's E2E model, with no plaintext
// cached-URL shortcut. thumbType selects the preview (proton.ThumbnailTypeDefault
// or proton.ThumbnailTypePhoto); pass 0 for "any".
func (protonDrive *ProtonDrive) DownloadThumbnail(ctx context.Context, linkID string, thumbType int) ([]byte, error) {
	protonDrive.removeLinkIDFromCache(linkID, false)

	link, err := protonDrive.getLink(ctx, linkID)
	if err != nil {
		return nil, err
	}

	parentNodeKR, err := protonDrive.getLinkKRByID(ctx, link.ParentLinkID)
	if err != nil {
		return nil, err
	}

	signatureVerificationKR, err := protonDrive.getSignatureVerificationKeyring([]string{link.SignatureEmail})
	if err != nil {
		return nil, err
	}

	nodeKR, err := link.GetKeyRing(parentNodeKR, signatureVerificationKR)
	if err != nil {
		return nil, err
	}

	sessionKey, err := link.GetSessionKey(nodeKR)
	if err != nil {
		return nil, err
	}

	revision, _, err := protonDrive.GetActiveRevisionWithAttrs(ctx, link)
	if err != nil {
		return nil, err
	}

	var target *proton.Thumbnail
	for i := range revision.Thumbnails {
		t := &revision.Thumbnails[i]
		if thumbType == 0 || t.Type == thumbType {
			target = t
			if thumbType != 0 {
				break
			}
		}
	}
	if target == nil {
		return nil, ErrNoThumbnail
	}

	urls, err := protonDrive.c.GetThumbnails(ctx, protonDrive.MainShare.VolumeID, []string{target.ThumbnailID})
	if err != nil {
		return nil, err
	}
	if len(urls) == 0 {
		return nil, ErrNoThumbnail
	}

	blockReader, err := protonDrive.c.GetBlock(ctx, urls[0].BareURL, urls[0].Token)
	if err != nil {
		return nil, err
	}
	defer blockReader.Close()

	encData, err := io.ReadAll(blockReader)
	if err != nil {
		return nil, err
	}

	// Verify the encrypted thumbnail matches the hash recorded in the
	// revision manifest before trusting the decrypted result.
	sum := sha256.Sum256(encData)
	if base64.StdEncoding.EncodeToString(sum[:]) != target.Hash {
		return nil, ErrDownloadedBlockHashVerificationFailed
	}

	plain, err := sessionKey.DecryptAndVerify(encData, signatureVerificationKR, crypto.GetUnixTime())
	if err != nil {
		return nil, err
	}

	return plain.GetBinary(), nil
}
