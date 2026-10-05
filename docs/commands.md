# Commands

Here is a full list of all available commands for the CNG Bot and what they do.

* `/dashboard`: Get a secure link to the web configuration dashboard for your server.
* `/map_admin`: Map a Discord role as a Bot Admin. Only allowed users can run commands.
* `/map_char_id`: Manually link an EVE Character ID to a Discord User ID.
* `/map_corp_id`: Manually map an EVE Corporation ID to a Discord Role ID.
* `/map_corp`: Auto map a Discord role to an EVE Corporation ID matching its exact name.
* `/map_alliance`: Map an EVE Alliance ID to a Discord Role ID.
* `/map_greet_role`: Map a Corporation or Alliance role to trigger a greeting message when auto assigned.
* `/seat_needed_roles`: Toggle requiring SeAT registration for a Corporation or Alliance role.
* `/req_corp_for_alliance`: Toggle requiring a mapped Corporation role for an Alliance role.
* `/rm_corp`: Remove an EVE Corporation mapping using its Discord Role.
* `/rm_corp_id`: Remove an EVE Corporation mapping using its EVE ID or Discord Role ID.
* `/rm_alliance`: Remove an EVE Alliance mapping using its Discord Role.
* `/rm_char_id`: Remove an EVE Character mapping using its EVE ID or Discord User ID.
* `/map_char`: Auto map a user based on their Discord nickname or username.
* `/sync`: Force an immediate manual verification of all mapped users roles.
* `/report`: Generate a report of unmapped users and automatically prune users who left the server.
* `/help`: Display list of available bot commands and usage.
* `/exclude_sync`: Exclude a Discord user from role synchronization checks.
* `/exclude_map`: Exclude a Discord user from automatic mapping and unmapped reporting.
* `/exclude_corp_standing`: Exclude a mapped Corporation role from receiving standing roles.
* `/exclude_2fa`: Exclude a mapped Corporation or Alliance role from requiring 2FA.
* `/toggle_2fa`: Toggle requiring a verification code via EVE mail to receive mapped Discord roles.
* `/set_log_channel`: Set the channel where logs should be posted.
* `/set_events_channel`: Set or disable the channel where newly created calendar events will be posted.
* `/set_mail_channel`: Set or disable the channel where EVE mails will be posted.
* `/set_donations_channel`: Set or disable the channel for donation announcements.
* `/set_donations_track_corporation`: Toggle tracking for an EVE Corporation via its mapped Discord role.
* `/donations_leaderboard`: Display the top 50 donors of all time to the tracked corporations.
* `/post_donations_leaderboard`: Post the leaderboard to the configured donations channel.
* `/import_donations`: Import pasted corporation wallet journal data to backfill donation history.
* `/toggle_logs`: Toggle posting role addition and removal logs to the designated channel.
* `/toggle_automap`: Toggle automatic character mapping of new server members on join.
* `/set_greeting`: Configure automatic greeting message for new members on join.
* `/set_guest`: Set the guest role applied to mapped users whose Corp or Alliance is not yet mapped.
* `/set_missing_seat_role`: Set a role to assign to users who need to log into SeAT but haven't.
* `/map_standing_terrible`: Map a Discord role to EVE Online Terrible standing.
* `/map_standing_bad`: Map a Discord role to EVE Online Bad standing.
* `/map_standing_neutral`: Map a Discord role to EVE Online Neutral standing.
* `/map_standing_good`: Map a Discord role to EVE Online Good standing.
* `/map_standing_excellent`: Map a Discord role to EVE Online Excellent standing.
* `/char_info`: Display detailed mapping, exclusion status, and automatically assigned roles for a Discord user.
* `/id`: Get the Discord ID and mention format for a user, channel, role, or emoji.
* `/courier_fee`: Calculates the fee for a courier contract.
