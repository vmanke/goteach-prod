"""goteach-salience — gelerntes Salienzmodul mit bidirektionaler Rueckkopplung.

Aus dem Verlauf einer Partie werden die Fenster bestimmt, in denen sich noch
etwas entscheidet. Das Modul waehlt aus; was ueber ein Fenster gesagt wird,
rechnet die Go-Seite deterministisch nach.
"""

from .contract import Game, Turn, Window

__all__ = ["Game", "Turn", "Window", "__version__"]

__version__ = "0.1.0"
