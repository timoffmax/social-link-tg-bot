package payload

/*
@link https://core.telegram.org/bots/api#getupdates
*/
type TgPlGetUpdates struct {
	Offset         int      `url:"offset,omitempty"`
	Limit          int      `url:"limit,omitempty"`
	Timeout        int      `url:"timeout,omitempty"`
	AllowedUpdates []string `url:"allowed_updates,omitempty"`
}
