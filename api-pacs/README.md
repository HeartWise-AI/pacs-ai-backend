# PACS AI Backend

PACS identity and user access management API using Firebase with multi-tenancy support.

## Prerequisites

- Google Cloud Platform account
- Firebase account
- Mailgun account
- Valid SSL certificate from hospital CA
- Docker and Docker Compose
- Make utility

## Project Setup

1. Create and setup project directory:
```bash
# Create project directory and clone repository
mkdir project-directory
cd project-directory
git clone https://github.com/HeartWise-AI/pacs-ai-backend.git
cd pacs-ai-backend
```

2. Configure Environment Files:
```bash
# For API PACS
cd api-pacs/
cp .env.example .env

# For Platform App
cd ../PACS-Ai/platform/app/
cp .env.example .env
```

## Google Cloud & Firebase Setup

1. Google Cloud Platform Setup:
   - Create account at https://cloud.google.com/
   - Create new project named `pacs-ai-prod` under heartwise.ai organization
   - Enable Identity Platform
   - Configure email/password provider
   - Disable user creation in Settings/Users
   - Enable tenants in Settings/Security
   - Add tenant named `mhi-prod` and enable email/password
   - Enable email/password provider for tenant

2. Firebase Setup:
   - Go to https://console.firebase.google.com
   - Create project `pacs-ai-prod` (disable Google Analytics)
   - Register web app named `pacs-ai-prod`
   - Configure Firebase credentials in `platform/.env` in the frontend app:
     ```
     APP_FIREBASE_API_KEY=<apiKey>
     APP_FIREBASE_AUTH_DOMAIN=<authDomain>
     APP_FIREBASE_PROJECT_ID=<projectId>
     APP_FIREBASE_STORAGE_BUCKET=<storageBucket>
     APP_FIREBASE_MESSAGING_SENDER_ID=<messagingSenderId>
     APP_FIREBASE_APP_ID=<appId>
     APP_FIREBASE_MEASUREMENT_ID=123456789
     ```

3. Firestore Database Setup:
   - Create database in Montreal region (northamerica-northeast1)
   - Start in Production Mode
   - Create `tenants` collection with following structure:
     ```
     Document ID: <Tenant_ID>
     Fields:
       - name: "Montreal Heart Institute"
     ```

## API Configuration

1. Update api-pacs/.env with:
```
APP_URL=https://YOUR_DOMAIN
ORTHANC_AET=YOUR_AET
```

2. Firebase Admin Setup:
   - Generate service account key from Firebase Console
   - Save as `pacs-ai-firebase-admin.json` in `api-pacs/configs/firebase/`
   - Update `FIREBASE_PROJECT_ID` in `.env` if you saved the file elsewhere

3. Mailgun Setup:
   - Create account at https://login.mailgun.com/login/
   - Add new domain and setup your DNS records according to Mailgun instructions
   - Configure in `.env`:
     ```
     MAILGUN_API_KEY=<your_api_key>
     MAILGUN_DOMAIN=<your_domain>
     MAILGUN_SENDER_EMAIL=<sender_email>
     ```

## DICOM Web Configuration

1. Update Platform Configuration:
   - In `PACS-Ai/platform/app/public/config/prod_pacs_ai.js`
   - Set DICOM Web endpoints:
     ```
     wadoUriRoot: "https://YOUR_DOMAIN/orthanc/dicom-web"
     qidoRoot: "https://YOUR_DOMAIN/orthanc/dicom-web"
     wadoRoot: "https://YOUR_DOMAIN/orthanc/dicom-web"
     ```

2. Configure Orthanc:
   - Update `pacs-ai-backend/orthanc/docker-compose.yml` to use the port you want Orthanc to listen on (see with PACS admins):
     ```yaml
     ports:
       - XXXX:XXXX
     ```

3. Update Nginx Configuration:
   - In `pacs-ai-backend/nginx/default.conf`
   - Set `server_name` to YOUR_DOMAIN
   - Install valid SSL certificate from hospital CA, otherwise default one will be generated automatically

## Running the Application

1. For development:
```bash
make
```

2. For production:
```bash
make up-prod
```

## Additional Commands

- Install dependencies: `make install`
- Run linter: `make lint` (requires golangci-lint)
- Run tests: `make test`
- Build binary: `make build` (output in bin/)

Note: Initial startup may take several minutes while containers are being built and initialized.
