package auth

import (
	"github.com/alexedwards/argon2id"
)

// HashPassword
func HashPassword(password string) (string, error){
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil{
		return "", err
	}
	return hash, nil
}

//CheckHash
func CheckHash(password, hash string) (bool, error) {
	match, err := argon2id.ComparePasswordAndHash(password, hash)
	if err != nil{
		return false, err
	}
	return match, nil
}
