package deps

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/config"
	"hectix/backend/internal/mail"
)

type Deps struct {
	DB   *pgxpool.Pool
	Cfg  config.Config
	Mail mail.Sender
}
