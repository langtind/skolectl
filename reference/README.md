# Reference

The Visma Flyt Skole guardian API as it was observed. The client in `internal/flyt` is written from this directory and never from a guess.

Guardians use the web client at `https://foresatt.visma.no/<customerKey>` and the phone app Flyt Foresatt. The school's own login page, `https://skole.visma.no/<customerKey>/Account/Login`, is for employees. Its link "Foresattportal" leads to `https://foresatt.visma.no/login/<customerKey>`, so it is the same service and the same login (checked 2026-10-13). The older `https://skole.visma.com/<customerKey>` only says that the portal has moved. Both talk to `https://api.foresatt.visma.no/`. There is no public documentation, and no open client was found on GitHub (2026-10-12).

## Verified

Observed 2026-10-12 with one guardian login at one school. Shapes only: no answer is kept here.

### Without a login

| Request | Answer |
| --- | --- |
| `GET /v1/customers/<customerKey>` | 200 with `name`, `customerKey`, `vismaConnectClientContext`, `hasFakeLogin`. 404 with an empty body for an unknown key |
| `GET /v1/session` | 401, empty body, `WWW-Authenticate: Bearer` |
| `GET /vismaconnect/<customerKey>/signin` | 302 to `https://connect.visma.com/connect/authorize`, and it sets the cookies `CookieSessionState`, `CookieNonce` and `CookieCustomerKey` |

The customer key is not the name of the school. A school is found by trying keys against `/v1/customers/<key>`. The old address `https://skole.visma.com/<key>` redirects to a "we have moved" page for a key that exists, and to the front page for one that does not.

### Login

The authorize address shows how the login works:

- `client_id=flyt-guardian-webapi`, `response_type=code`, `response_mode=form_post`, `redirect_uri=https://api.foresatt.visma.no/VismaConnect/callback`.
- Scopes: `openid profile email nnin graphapi:read offline_access flyt-guardian-web-api:all flyt-skole-guardian-api:all`.
- `acr_values=urn:idp:id-porten urn:idp:vismaconnect:method:pwdless`: the user logs in with ID-porten (BankID, MinID, Buypass).

The code goes back to the API, which answers with cookies on `api.foresatt.visma.no`. All are session cookies, `HttpOnly`, `Secure`, `SameSite=Strict`:

| Cookie | What it is |
| --- | --- |
| `CookieAccessToken` | A JWT from `https://connect.visma.com`, `typ` `at+JWT`, valid for 5 minutes. Claims: `exp`, `iat`, `auth_time`, `client_id`, `scope`, `sub`, `sid`, `idp`, `amr`, `acr`, `llt`, `created_at` |
| `CookieRefreshToken` | 64 hex characters |
| `GwebId` | 136 characters. Does not change during the login |
| `CookieCustomerKey` | The customer key. Not needed for a request |

What a request needs, tested with `GET /v1/session`:

| Cookies sent | Answer |
| --- | --- |
| access token and `GwebId` | 200 |
| access token alone, or with the refresh token or the customer key | 401 |
| `GwebId` alone | 401 |
| the access token as `Authorization: Bearer`, no cookies | 401 |
| access token that has run out and `GwebId` | 401 |
| `GwebId` and the refresh token, with or without an access token that has run out | 200, and `Set-Cookie` for `CookieAccessToken` and `CookieRefreshToken` |

So renewal is not a request of its own. The API renews when it is sent the refresh token, and hands back a new access token valid for another 5 minutes. `auth_time` stays at the time of the ID-porten login. The refresh token it handed back was the same one, in four renewals over 25 minutes.

Not known: how long the refresh token lasts, and whether it ever changes. `llt` in the access token is a time about two months before the login, so it is not the expiry.

### Requests

Every request is sent with the header `Requested-By: XMLHttpRequest`, as the web client does.

Times come in two forms. `firstLoginDate` on the session has a `Z`. Every time on a message, a notice, a form, an absence and a document has no zone, with zero to three decimals, such as `2026-10-12T20:05:58.123`. They are read as Oslo time. The hours that messages from the school fall in fit that, but nothing proves it yet.

An error body is `{"message": "Internal server error", "errorCode": "<uuid>"}`, whatever the status. A wrong query value is a 400 with `{"<parameter>": ["..."]}`.

| Request | Answer |
| --- | --- |
| `GET /v1/session` | `firstName`, `lastName`, `userId`, `firstLoginDate`, `school{organizationId, guardianEmail}`, `municipality{name, organizationNumber, customerKey}`, `guardianIdentityNumber`, `isImpersonated` |
| `GET /v2/children` | `children[]` with `firstName`, `lastName`, `birthDate`, `photoUrl`, `school{pupilId, schoolId, level, organizationName}` |
| `GET /v2/badges` | A list of `{pupilId, badges[{area, count}]}` |
| `GET /vfs/v1/pupils/<pupil>/portal-areas` | A list of `{area, subareas[]}`: what the school has turned on |
| `GET /vfs/v1/pupils/<pupil>/disabled-menu-items` | A list of area numbers |

A pupil id that is not the guardian's is answered with 403. An area the school has turned off is 403 too: `remarks/years`, `evaluations/years` and `additional-info` were, here.

Area numbers, from the web client: 0 digital forms, 1 evaluations, 2 absence, 3 applications, 4 documents and resources, 5 grades, 9 remarks, 10 messages, 11 announcements, 12 consents, 13 forms, 14 timetable, 15 SFO presence, 16 groups and information.

Messages. `<pupil>` is the pupil id.

| Request | Answer |
| --- | --- |
| `GET .../messages/message-info` | `directMessagesUnreadCount`, `groupMessagesUnreadCount` |
| `GET .../messages/direct` | One row for each employee: `idMessage`, `sentDate`, `title`, `messageText` (cut at 80 characters), `sender`, `employeeId`, `unreadCount`. `sender` is the employee, also when the guardian wrote last. In no order |
| `GET /vfs/v2/pupils/<pupil>/messages/direct/<employee>` | `canReply`, `messages[]` with `idMessage`, `title`, `messageText`, `sentDate`, `readDate`, `participantType`, `attachments[]`. Latest first. `participantType` 1 is from the school, 2 from the guardian. Plain text with line breaks, no HTML. An employee id that does not exist is a 500 |
| `GET .../messages/group` | `conversationId`, `title`, `concerningPupils`, `lastMessage` (whole), `createdOn`, `lastMessageDate`, `unreadMessageCount`, `createdByPersonBaseId`, `isReadonly`. Latest first |
| `GET .../messages/group/<conversation>` | `conversationId`, `title`, `createdOn`, `isReadonly`, `participants[]` (names), `concerningPersons[]`, `messages[]` with `senderId`, `sentOn`, `sentBy`, `sentByInitials`, `message`, `sentByMe`, `isRemovalMessage`, `lastReadBy[]`, `lastReadByInfo[]`, `attachments[]` with `messageId`, `attachmentId`, `fileName`, `contentType`, `contentLength`. A conversation that does not exist is a 500 |
| `GET .../messages/group/<conversation>/attachment/<attachment>` | The file, with `Content-Type` and `Content-Length` and no file name. Fetched with the login cookies, from the API host |
| `GET .../messages/receivers` | A list of `{role, roleType, receivers[{receiverId, receiverRole, receiverName}]}` |

Reading a thread does not change the unread counts.

The rest, all under `/vfs/v1/pupils/<pupil>/` unless a full path is given:

| Request | Answer |
| --- | --- |
| `GET notifications/unread-count` | A list of `{messageType, unreadCount}` |
| `GET notifications/<kind>` | Kind 0 is notifications and 1 announcements, as the web client names them. 2 is a 400. `idMessage`, `sentDate`, `title`, `sender`, `messageText`, `isUnread` |
| `GET absences/years` | `municipalityYearId`, `fromYear`, `toYear`, `isCurrent`. Next year is listed too |
| `GET absences/<year>?term=` | `id`, `type`, `status`, `date`, `comment`, `modifiedBy`, `daysAbsent`. `term` is 1 for fall, 2 for spring, left out for the year. 0 is a 400. Only type 1 with status 1 was seen: a whole day, documented |
| `GET absences/<year>/summary?term=` | `undocumentedDays`, `undocumentedMinutes`, `documentedDays`, `documentedMinutes`, `totalDays`, `totalMinutes` |
| `GET digital-forms` | `id`, `title`, `deadline`, `lastModifiedOn`, `publishedOn`, `lastModifiedBy`, `isAnswered`, `fromDifferentSchool`, `schoolName`, `isDeadlinePassed` |
| `GET digital-forms/<form>` | The same and `publishText`, `furtherInformation`, `type`, `commentAllowed`, `maximumNumberOfChoices`, `requiredNumberOfChoices`, `alternatives[{id, order, type, text}]`, `attachments[]`, `answers{lastModifiedBy, lastModifiedOn, answers[{alternativeId, answerType}]}`, `isReadOnly` |
| `GET consents` | `canReply`, `consents[]` with `id`, `title`, `status`, `modifiedOn`, `modifiedBy`, `content`. Status 0 is not answered, 1 yes, 2 no |
| `GET applications` | `requestId`, `createdDate`, `answeredBy`, `answeredTime`, `status`, `applicationType` |
| `GET applications/<request>` | The same and `requestedBy`, `fromDate`, `toDate`, `causeName`, `parentComment`, `employeeComment`, `caseWorkers[]`, `isReadByApplicant`, `hasAnswerDocument`, `hasApplicationDocument` |
| `GET applicationTypes` | A list of `{type, name}` |
| `GET /vfs/v1/schools/<school>/applicationTypes` | A list of `{type, name}`: the types this school takes |
| `GET /vfs/v1/schools/<school>/applicationTypes/<type>` | `name`, `type`, `hasRulesDocument`, `template{applicationInfoText, archiveText}`. Both texts are plain text with line breaks |
| `GET /vfs/v1/schools/<school>/applicationTypes/<type>/causes` | A list of `{id, name, isDefault, onlyForLevelZero}` |
| `GET applications/type/<type>` | The same rows as `applications`, for one type |
| `GET groups` | `contactTeacher{fullName}`, `classGroup{name, level, responsibles[]}`, `subjectGroups[{subject{name}, name, responsibles[]}]`, `mixedGroups[]`, `sfoGroups[]` |
| `GET pupil-info` | The child's national identity number, address and parents. Not used |
| `GET documents/years` | `schoolYearId`, `fromYear`, `toYear`, `isCurrent` |
| `GET documents/years/<year>` | `archiveFileId`, `documentTypeId`, `title`, `createdBy`, `createdOn`, `schoolName` |
| `GET documents/types` | `title`, `documentTypeId` |
| `GET grades/years` | An empty list here |
| `GET timetable?selectedDate=yyyy-mm-dd` | An empty list here, so its shape is not known |
| `GET /vfs/v1/schools` and `/vfs/v1/schools/<school>` | `name`, `phoneNumber`, `emailAddress`, `street`, `zip`, `city`, `homepage` |
| `GET /vfs/v1/schools/<school>/links` | `title`, `url` |
| `GET /vfs/v1/schools/<school>/documents` | `documentId`, `title` |
| `GET /vfs/v1/schools/<school>/documents/<document>` | The file |

Application types and statuses are numbers. The names are in `internal/flyt/school.go`, taken from the web client.

## Read in the web client's code

From the bundle at `https://foresatt.visma.no/`, version `2026.10.101.184`. This is what the service's own client sends. No request below has been sent by skolectl for real. Move a line to "Verified" when one has.

| Request | Body |
| --- | --- |
| `POST .../messages/direct/<employee>/reply` | Multipart form, field `request` holds the JSON `{"text": "..."}` |
| `POST .../messages/group/<conversation>/reply` | The same |
| `POST .../messages/direct/<employee>/mark-as-read` | None |
| `POST .../messages/group/<conversation>/mark-as-read` | None |
| `POST .../messages/direct/send` | Multipart form, field `request` holds `{"text": "...", "messageReceiverIds": [...]}`. Not built |
| `POST .../messages/group/create` | Multipart form, field `request` holds `{"text": "...", "receiverIds": [...]}`. Not built |
| `GET .../messages/direct/<employee>/attachment/<id>` | Not built: no direct message with a file was seen, so the name of the id is not known |
| `POST .../notifications/mark-as-read/<id>` | Not built |
| `POST .../consents`, `POST .../digital-forms/<form>/submit-answer` | Not built |
| `POST /vfs/v1/schools/<school>/applications` | An application. Multipart form: the field `request` holds the JSON below, and each file is a field `attachments`. What the service answers is not known |
| `GET /vfs/v1/schools/<school>/applicationTypes/<type>/rulesDocument` | The school's rules for one type, as a file. Not built |
| `GET applications/<request>/attachment`, `.../answer-document`, `.../application-document` | Files on an application. Not built |
| `GET /v1/session/signOut?returnUrl=` | Log out. The browser is sent there. Not used: `logout` only forgets the login |

An application for leave, as the web client builds it:

```json
{"applicationType": 1, "comment": "<reason>", "applicationTypeCauseId": 5, "fromDate": "2026-11-05T00:00:00", "toDate": "2026-11-05T00:00:00", "hasReadRules": null, "pupilInYearIds": [1001]}
```

- `pupilInYearIds` holds the pupil id of the child, and of any sibling the guardian ticked. skolectl sends one child.
- `hasReadRules` is `true` when the school has a rules document and the guardian ticked the box. Without a rules document the box is disabled and the value is `null`.
- Dates are written as `YYYY-MM-DDTHH:mm:ss`.
- What the form checks before it sends: a cause is picked, the first day is today or later, the last day is the first day or later and at most 60 days after it, and the reason is filled in and at most 2000 characters.
- Files: at most 3, each at most 5 MB, and `.pdf`, `.png`, `.jpg` or `.jpeg`, checked by the MIME type the browser gives the file.
- There is no draft step and no key that tells a second try from a new application. The form only blocks its own button while the request runs. So skolectl looks for an application with the same days before it sends, and looks the application up afterwards.

The web client logs out after a time with no mouse or keyboard activity. That is a timer in the browser, and nothing the API asks for.

## Unverified

- That times without a zone are Oslo time.
- How long the refresh token lasts. It renewed after 11 hours with no request in between (2026-10-13). Read `skolectl status` some days after a login to find the limit.
- What an hour absence looks like: `type` other than 1, and any fields a day absence lacks.
- Everything about sending an application: no application has been sent by skolectl. Whether the service checks the same limits as the form, what it answers, and how soon the application shows in `applications`.
- Whether `pupilInYearIds` takes the pupil id that `/v2/children` gives. The web client passes that id.
- What sending the same reply twice does. The code assumes it sends two messages.
- Whether the phone app logs in another way, with a refresh token of its own.
