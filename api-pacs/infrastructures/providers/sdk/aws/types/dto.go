package types

// Config aws basic config
type Config struct {
	Region    string
	AccessID  string
	SecretKey string
}

type SESSendEmailRequest struct {
	Subject          string
	CCAddresses      []string
	ToAddresses      []string
	HTMLMessage      string
	PlainTextMessage string
	SourceEmail      string
}

type S3GetPresignedURLRequest struct {
	Bucket              string
	Key                 string
	ExpirationInMinutes int
}

type S3UploadRequest struct {
	Bucket        string
	Key           string
	Body          []byte
	ContentLength int64
	Metadata      map[string]*string
	PublicRead    bool
}
