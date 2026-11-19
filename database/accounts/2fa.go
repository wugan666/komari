package accounts

import (
	"errors"
	"image"

	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/pquerna/otp/totp"
)

var (
	TwoFactorIssuer            = "Komari Monitor"
	ErrTwoFactorAlreadyEnabled = errors.New("2FA is already enabled; disable it with the current code before setting a new secret")
)

func Generate2Fa() (string, image.Image, error) {
	otp, err := totp.Generate(totp.GenerateOpts{
		Issuer:      TwoFactorIssuer,
		AccountName: "komari",
	})
	if err != nil {
		return "", nil, err
	}
	img, err := otp.Image(250, 250)
	if err != nil {
		return "", nil, err
	}
	return otp.Secret(), img, nil
}

func Enable2Fa(uuid, secret string) error {
	db := dbcore.GetDBInstance()
	// Check and write atomically: concurrent setup requests must never replace
	// an existing factor without verifying that factor first.
	result := db.Model(&models.User{}).Where("uuid = ? AND (two_factor = ? OR two_factor IS NULL)", uuid, "").Update("two_factor", secret)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTwoFactorAlreadyEnabled
	}
	return nil
}

func Verify2Fa(uuid, code string) (bool, error) {
	db := dbcore.GetDBInstance()
	var user models.User
	if err := db.Where("uuid = ?", uuid).First(&user).Error; err != nil {
		return false, err
	}
	// 用户未启用2FA时视为校验失败
	return user.TwoFactor != "" && totp.Validate(code, user.TwoFactor), nil
}

func Disable2Fa(uuid string) error {
	db := dbcore.GetDBInstance()
	return db.Model(&models.User{}).Where("uuid = ?", uuid).Update("two_factor", "").Error
}
