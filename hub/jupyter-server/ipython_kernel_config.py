# Configuration file for ipython-kernel.

c = get_config()  # noqa

## Set the color scheme (NoColor, Neutral, Linux, or LightBG).
#  Choices: any of ['Neutral', 'NoColor', 'LightBG', 'Linux'] (case-insensitive)
#  Default: 'Neutral'
c.InteractiveShell.colors = "NoColor"

# Truncate large collections (lists, dicts, tuples, sets) to this size.
# Set to 0 to disable truncation.
#  Default: 1000
c.PlainTextFormatter.max_seq_length = 0

